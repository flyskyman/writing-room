package main

// 稿件工作台（2026-10-14 人間の依頼。医学写作）。
// 应用层权威状态：稿件版本、修改候选、作者裁定。存在 logs/manuscript.json，与会话（chat-*.jsonl）分开保存。
// 每轮把当前版本与有效裁定冻结进提示词；代理只返回候选（```candidate 围栏块），正式写入只在作者接受后由本程序完成。
// 每次调用留下上下文清单（logs/context.jsonl）与输入记录（logs/prompts/）。

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type MsVersion struct {
	ID     int    `json:"id"`
	Text   string `json:"text"`
	Source string `json:"source"` // author / candidate:<候选ID> / undo:<版本ID>
	Note   string `json:"note,omitempty"`
	Time   string `json:"time"`
}

type MsCandidate struct {
	ID        int    `json:"id"`
	MsgID     int    `json:"msg_id"`
	Turn      string `json:"turn"`
	Agent     string `json:"agent"`
	Model     string `json:"model,omitempty"`
	Base      int    `json:"base"`      // 提示词快照里的稿件版本
	Decisions int    `json:"decisions"` // 提示词快照里最后一条裁定的 ID（0 为无）
	Text      string `json:"text"`
	Status    string `json:"status"` // pending / accepted / rejected
	Time      string `json:"time"`
	// 派生字段（序列化时计算，不保存）
	Stale          bool `json:"stale"`           // 依据版本的正文与当前版本不同（接受时同样校验）
	MissedDecision int  `json:"missed_decision"` // 快照之后新增的第一条裁定 ID（0 为无）
}

type MsDecision struct {
	ID    int    `json:"id"`
	MsgID int    `json:"msg_id"`
	Text  string `json:"text"`
	Time  string `json:"time"`
}

type Manuscript struct {
	Versions   []MsVersion   `json:"versions"`
	Candidates []MsCandidate `json:"candidates"`
	Decisions  []MsDecision  `json:"decisions"`
}

var (
	ErrMsStale          = errors.New("基准版本已不是当前版本，请刷新后重试")
	ErrMsNotFound       = errors.New("候选不存在")
	ErrMsNotPending     = errors.New("候选已处理")
	ErrMsMissedDecision = errors.New("该候选产生时未见最新裁定，不能直接接受")
	ErrMsEmpty          = errors.New("稿件为空")
	ErrMsNoUndo         = errors.New("没有可撤销的版本")
)

const candidateLang = "candidate"

func (r *Room) manuscriptPath() string { return filepath.Join(r.logDir, "manuscript.json") }

func (r *Room) loadManuscriptLocked() {
	b, err := os.ReadFile(r.manuscriptPath())
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			r.log.Warn("manuscript.load", "error", err.Error())
		}
		return
	}
	if err := json.Unmarshal(b, &r.ms); err != nil {
		r.log.Warn("manuscript.load", "error", err.Error())
	}
}

// saveManuscriptLocked 保存并广播。保存失败只记日志（内存状态仍是权威）
func (r *Room) saveManuscriptLocked() {
	b, _ := json.MarshalIndent(r.ms, "", "  ")
	if err := writeStateFile(r.manuscriptPath(), b); err != nil {
		r.log.Error("manuscript.save", "error", err.Error())
	}
	snap := r.manuscriptSnapshotLocked()
	r.broadcastLocked(Event{Type: "manuscript", Manuscript: &snap})
}

func (r *Room) msCurrentLocked() (MsVersion, bool) {
	if len(r.ms.Versions) == 0 {
		return MsVersion{}, false
	}
	return r.ms.Versions[len(r.ms.Versions)-1], true
}

func (r *Room) msVersionLocked(id int) (MsVersion, bool) {
	for _, v := range r.ms.Versions {
		if v.ID == id {
			return v, true
		}
	}
	return MsVersion{}, false
}

func (r *Room) msCurrentIDLocked() int {
	cur, _ := r.msCurrentLocked()
	return cur.ID
}

func (r *Room) msLastDecisionLocked() int {
	if n := len(r.ms.Decisions); n > 0 {
		return r.ms.Decisions[n-1].ID
	}
	return 0
}

// manuscriptSnapshotLocked 返回带派生字段的副本
func (r *Room) manuscriptSnapshotLocked() Manuscript {
	cur, _ := r.msCurrentLocked()
	snap := Manuscript{Versions: append([]MsVersion{}, r.ms.Versions...), Decisions: append([]MsDecision{}, r.ms.Decisions...)}
	for _, c := range r.ms.Candidates {
		base, ok := r.msVersionLocked(c.Base)
		c.Stale = !ok || base.Text != cur.Text
		c.MissedDecision = 0
		for _, d := range r.ms.Decisions {
			if d.ID > c.Decisions {
				c.MissedDecision = d.ID
				break
			}
		}
		snap.Candidates = append(snap.Candidates, c)
	}
	return snap
}

func (r *Room) ManuscriptSnapshot() Manuscript {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.manuscriptSnapshotLocked()
}

func (r *Room) addVersionLocked(text, source, note string) MsVersion {
	v := MsVersion{ID: len(r.ms.Versions) + 1, Text: text, Source: source, Note: note, Time: time.Now().Format("2006-01-02 15:04:05")}
	r.ms.Versions = append(r.ms.Versions, v)
	r.log.Info("manuscript.version", "id", v.ID, "source", source, "chars", len([]rune(text)))
	return v
}

// SetManuscript 由作者写入新版本（导入或手工编辑）。base 必须等于当前版本 ID（没有版本时为 0）
func (r *Room) SetManuscript(text, note string, base int) (MsVersion, error) {
	text = strings.TrimSpace(strings.ReplaceAll(text, "\r\n", "\n"))
	if text == "" {
		return MsVersion{}, ErrMsEmpty
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	cur, _ := r.msCurrentLocked()
	if base != cur.ID {
		return MsVersion{}, ErrMsStale
	}
	v := r.addVersionLocked(text, "author", note)
	r.saveManuscriptLocked()
	return v, nil
}

// AcceptCandidate 接受候选 id。重新校验基准：候选依据版本的正文须与当前正文相同，且 base 须为当前版本 ID。
// text 非空时为作者微调后的文本（仍记为该候选的接受）
func (r *Room) AcceptCandidate(id, base int, text string) (MsVersion, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	i := r.candidateIndexLocked(id)
	if i < 0 {
		return MsVersion{}, ErrMsNotFound
	}
	c := &r.ms.Candidates[i]
	if c.Status != "pending" {
		return MsVersion{}, ErrMsNotPending
	}
	snap := r.manuscriptSnapshotLocked()
	cur, _ := r.msCurrentLocked()
	for _, sc := range snap.Candidates {
		if sc.ID != id {
			continue
		}
		if sc.MissedDecision > 0 {
			return MsVersion{}, ErrMsMissedDecision
		}
		if sc.Stale || base != cur.ID {
			return MsVersion{}, ErrMsStale
		}
	}
	text = strings.TrimSpace(strings.ReplaceAll(text, "\r\n", "\n"))
	note := ""
	if text == "" {
		text = c.Text
	} else if text != c.Text {
		note = "作者微调"
	}
	c.Status = "accepted"
	v := r.addVersionLocked(text, fmt.Sprintf("candidate:%d", id), note)
	r.postLocked("system", fmt.Sprintf("作者接受了 @%s 的候选 #%d（%s），稿件更新为 v%d。", c.Agent, c.ID, firstNonEmpty(note, "原样"), v.ID), "system")
	r.saveManuscriptLocked()
	return v, nil
}

func (r *Room) RejectCandidate(id int) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	i := r.candidateIndexLocked(id)
	if i < 0 {
		return ErrMsNotFound
	}
	if r.ms.Candidates[i].Status != "pending" {
		return ErrMsNotPending
	}
	r.ms.Candidates[i].Status = "rejected"
	r.saveManuscriptLocked()
	return nil
}

// UndoManuscript 撤销：追加一个正文等于上一版本的新版本（历史只增不删，基准校验仍按版本 ID）
func (r *Room) UndoManuscript() (MsVersion, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := len(r.ms.Versions)
	if n < 2 {
		return MsVersion{}, ErrMsNoUndo
	}
	cur, prev := r.ms.Versions[n-1], r.ms.Versions[n-2]
	v := r.addVersionLocked(prev.Text, fmt.Sprintf("undo:%d", cur.ID), fmt.Sprintf("恢复 v%d 正文", prev.ID))
	r.postLocked("system", fmt.Sprintf("作者撤销了 v%d，稿件 v%d 的正文恢复为 v%d。", cur.ID, v.ID, prev.ID), "system")
	r.saveManuscriptLocked()
	return v, nil
}

func (r *Room) candidateIndexLocked(id int) int {
	for i, c := range r.ms.Candidates {
		if c.ID == id {
			return i
		}
	}
	return -1
}

// addDecisionLocked 记录作者裁定（人间发言 m）。之后的每轮提示词都带上
func (r *Room) addDecisionLocked(m Message) MsDecision {
	d := MsDecision{ID: len(r.ms.Decisions) + 1, MsgID: m.ID, Text: m.Text, Time: m.Time}
	r.ms.Decisions = append(r.ms.Decisions, d)
	r.log.Info("manuscript.decision", "id", d.ID, "msg_id", m.ID)
	r.saveManuscriptLocked()
	return d
}

// addCandidatesLocked 从代理回复 m 中取出 ```candidate 围栏块，登记为候选（依据 base 版本、裁定 dec）
func (r *Room) addCandidatesLocked(m Message, turn string, base, dec int) int {
	n := 0
	for _, b := range extractCodeBlocks(m.Text) {
		if b.Lang != candidateLang || strings.TrimSpace(b.Code) == "" {
			continue
		}
		c := MsCandidate{ID: len(r.ms.Candidates) + 1, MsgID: m.ID, Turn: turn, Agent: m.From, Model: m.Model, Base: base, Decisions: dec,
			Text: strings.TrimSpace(b.Code), Status: "pending", Time: m.Time}
		r.ms.Candidates = append(r.ms.Candidates, c)
		n++
	}
	if n > 0 {
		r.log.Info("manuscript.candidates", "msg_id", m.ID, "agent", m.From, "count", n, "base", base)
		r.saveManuscriptLocked()
	}
	return n
}

// writeManuscriptPrompt 把冻结的共享材料写进提示词：当前稿件版本与全部裁定。返回快照用的版本 ID 与最后裁定 ID
func (r *Room) writeManuscriptPrompt(b *strings.Builder) (ver, dec int) {
	cur, ok := r.msCurrentLocked()
	if !ok {
		return 0, r.msLastDecisionLocked()
	}
	b.WriteString("--- 共享材料（工作台冻结快照。以此为准，不要凭记忆引用旧版本）---\n")
	fmt.Fprintf(b, "稿件 v%d：\n%s\n\n", cur.ID, cur.Text)
	if len(r.ms.Decisions) > 0 {
		b.WriteString("作者已确认的裁定（按时间，后者优先）：\n")
		for _, d := range r.ms.Decisions {
			fmt.Fprintf(b, "- 裁定 %d（#%d）：%s\n", d.ID, d.MsgID, d.Text)
		}
	} else {
		b.WriteString("作者尚未作出裁定。\n")
	}
	b.WriteString("--- 共享材料到此 ---\n")
	b.WriteString("提议修改时，把完整替换后的稿件正文放在一个 ```candidate 围栏块里（英文稿件保留英文），理由写在块外；")
	b.WriteString("数字、定义、引文或结论强度的任何改动须在块外显著标明，不得伪装成纯语言修改。不提议修改就不写该块。\n\n")
	return cur.ID, r.msLastDecisionLocked()
}

// ---- 上下文清单与输入记录 ----------------------------------------------------------

type turnManifest struct {
	Turn        string `json:"turn"`
	Agent       string `json:"agent"`
	ModelSel    string `json:"model_sel"`
	Permission  string `json:"permission"`
	Resumed     bool   `json:"resumed"` // 续接已有 CLI 会话
	MsVersion   int    `json:"ms_version"`
	Decisions   int    `json:"decisions"` // 快照里最后一条裁定 ID
	DeltaIDs    []int  `json:"delta_ids"` // 本轮传入的新消息 ID
	ReadUpTo    int    `json:"read_upto"`
	PromptChars int    `json:"prompt_chars"`
	PromptFile  string `json:"prompt_file"`
	Time        string `json:"time"`
	Prompt      string `json:"prompt,omitempty"` // 只在 API 返回时填
}

func (r *Room) contextPath() string { return filepath.Join(r.logDir, "context.jsonl") }

// writeManifest 写入输入记录（已脱敏：作业令牌已替换）与清单。不持锁调用
func (r *Room) writeManifest(m turnManifest, prompt string) {
	dir := filepath.Join(r.logDir, "prompts")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		r.log.Error("manifest.write", "error", err.Error())
		return
	}
	m.PromptFile = filepath.Join("prompts", m.Turn+"-"+m.Agent+".txt")
	m.PromptChars = len([]rune(prompt))
	m.Time = time.Now().Format("2006-01-02 15:04:05")
	if err := os.WriteFile(filepath.Join(r.logDir, m.PromptFile), []byte(prompt), 0o644); err != nil {
		r.log.Error("manifest.write", "error", err.Error())
	}
	b, _ := json.Marshal(m)
	f, err := os.OpenFile(r.contextPath(), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		r.log.Error("manifest.write", "error", err.Error())
		return
	}
	defer f.Close()
	f.Write(append(b, '\n'))
}

// Manifest 返回 turn 的清单与输入全文
func (r *Room) Manifest(turn string) (turnManifest, bool) {
	f, err := os.Open(r.contextPath())
	if err != nil {
		return turnManifest{}, false
	}
	defer f.Close()
	var found turnManifest
	ok := false
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1024*1024), 16*1024*1024)
	for sc.Scan() {
		var m turnManifest
		if json.Unmarshal(sc.Bytes(), &m) == nil && m.Turn == turn {
			found, ok = m, true // 同名 turn 以最后一条为准
		}
	}
	if !ok {
		return turnManifest{}, false
	}
	if b, err := os.ReadFile(filepath.Join(r.logDir, found.PromptFile)); err == nil {
		found.Prompt = string(b)
	}
	return found, true
}

// ---- HTTP ----------------------------------------------------------------------

func (room *Room) handleGetManuscript(w http.ResponseWriter, r *http.Request) {
	writeData(w, http.StatusOK, room.ManuscriptSnapshot())
}

func (room *Room) handlePutManuscript(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Text string `json:"text"`
		Note string `json:"note"`
		Base int    `json:"base"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil {
		writeError(w, r, http.StatusBadRequest, "INVALID_JSON", "JSONを解析できません")
		return
	}
	v, err := room.SetManuscript(body.Text, body.Note, body.Base)
	room.writeMsResult(w, r, v, err)
}

func (room *Room) handleAcceptCandidate(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(r.PathValue("id"))
	var body struct {
		Base int    `json:"base"`
		Text string `json:"text"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil && !errors.Is(err, io.EOF) {
		writeError(w, r, http.StatusBadRequest, "INVALID_JSON", "JSONを解析できません")
		return
	}
	v, err := room.AcceptCandidate(id, body.Base, body.Text)
	room.writeMsResult(w, r, v, err)
}

func (room *Room) handleRejectCandidate(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(r.PathValue("id"))
	room.writeMsResult(w, r, nil, room.RejectCandidate(id))
}

func (room *Room) handleUndoManuscript(w http.ResponseWriter, r *http.Request) {
	v, err := room.UndoManuscript()
	room.writeMsResult(w, r, v, err)
}

func (room *Room) writeMsResult(w http.ResponseWriter, r *http.Request, data any, err error) {
	switch {
	case err == nil:
		writeData(w, http.StatusOK, data)
	case errors.Is(err, ErrMsStale):
		writeError(w, r, http.StatusConflict, "MS_STALE", err.Error())
	case errors.Is(err, ErrMsMissedDecision):
		writeError(w, r, http.StatusConflict, "MS_MISSED_DECISION", err.Error())
	case errors.Is(err, ErrMsNotPending):
		writeError(w, r, http.StatusConflict, "MS_NOT_PENDING", err.Error())
	case errors.Is(err, ErrMsNotFound):
		writeError(w, r, http.StatusNotFound, "MS_NOT_FOUND", err.Error())
	case errors.Is(err, ErrMsNoUndo):
		writeError(w, r, http.StatusConflict, "MS_NO_UNDO", err.Error())
	default:
		writeError(w, r, http.StatusBadRequest, "MS_INVALID", err.Error())
	}
}

// handleExportManuscript 以 Markdown 导出当前版本（副本；不触碰任何原稿文件）
func (room *Room) handleExportManuscript(w http.ResponseWriter, r *http.Request) {
	room.mu.Lock()
	cur, ok := room.msCurrentLocked()
	room.mu.Unlock()
	if !ok {
		writeError(w, r, http.StatusNotFound, "MS_NOT_FOUND", ErrMsEmpty.Error())
		return
	}
	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=manuscript-v%d.md", cur.ID))
	fmt.Fprintf(w, "<!-- manuscript v%d · %s · source: %s -->\n\n%s\n", cur.ID, cur.Time, cur.Source, cur.Text)
}

func (room *Room) handleGetTurn(w http.ResponseWriter, r *http.Request) {
	m, ok := room.Manifest(r.PathValue("turn"))
	if !ok {
		writeError(w, r, http.StatusNotFound, "TURN_NOT_FOUND", "没有这一轮的上下文清单")
		return
	}
	writeData(w, http.StatusOK, m)
}
