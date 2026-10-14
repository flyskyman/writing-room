package main

// 「要約して新しい会話」: 長くなった会話を1人のエージェントに要約させ、新しい会話の最初に要約を置いて始め直す。
// 各 CLI の会話（セッション）を作り直すので、以降のターンで過去の全履歴を読み直すトークン消費を抑えられる。
// 元の会話のログファイルはそのまま残す。

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

var (
	ErrSummarizing   = errors.New("正在生成总结")
	ErrNothingToSumm = errors.New("没有可总结的发言")
)

// summaryMaxChars を超える会話は、後ろ（新しい発言）から summaryMaxChars 文字だけを要約に渡す
const summaryMaxChars = 200000

const summaryInstruction = "以下是作者与多个 AI 代理的群聊记录。" +
	"为把本对话交接到新对话，请按下面三个标题，用中文要点简洁总结。" +
	"文件名、函数名、设置值等继续工作所需的具体信息不要省略。不要开场白和结束语。\n" +
	"## 已决事项\n## 未完成的工作（有负责人、完成条件则一并写）\n## 注意点\n\n--- 记录 ---\n"

// summaryDeltaInstruction は前回の議事録と、その後の発言から議事録を作り直すときの指示（案10）。
// 全文を渡し直さないので要約のトークンが減り、後ろから summaryMaxChars 文字で切ったときに古い決定事項が落ちることもない
const summaryDeltaInstruction = "以下是作者与多个 AI 代理群聊的上一份纪要，以及之后的新发言记录。" +
	"为把本对话交接到新对话，请把新发言的内容合并进上一份纪要，按下面三个标题，用中文要点简洁写出。" +
	"「已决事项」保留上一份的全部条目，新决定追加在后面（被撤销或更改的条目不删，注明即可）。" +
	"「未完成的工作」和「注意点」请根据新发言更新（去掉已完成的）。" +
	"文件名、函数名、设置值等继续工作所需的具体信息不要省略。不要开场白和结束语。\n" +
	"## 已决事项\n## 未完成的工作（有负责人、完成条件则一并写）\n## 注意点\n\n--- 上一份纪要 ---\n"

const summaryDeltaSeparator = "\n\n--- 上一份纪要之后的新记录 ---\n"

// summarizerLocked は要約を担当するエージェントを選ぶ。進行役が参加できればその人、いなければ最初の参加可能なエージェント。
func (r *Room) summarizerLocked() *Agent {
	if a := r.agent(r.leader); a != nil && a.active() {
		return a
	}
	for _, a := range r.agents {
		if a.active() {
			return a
		}
	}
	return nil
}

// transcriptLocked は要約に渡す会話の記録を作る（パスは除く）
func (r *Room) transcriptLocked() string {
	return transcriptOf(r.messages)
}

// transcriptOf は発言の一覧を要約用の記録にする（パスは除く）。summaryMaxChars を超えたら後ろを残す
func transcriptOf(msgs []Message) string {
	var b strings.Builder
	for _, m := range msgs {
		if m.Kind == "pass" {
			continue
		}
		fmt.Fprintf(&b, "[#%d %s %s]: %s\n\n", m.ID, m.Time, m.From, m.Text)
	}
	s := b.String()
	if rs := []rune(s); len(rs) > summaryMaxChars {
		s = "…(前半省略)…\n" + string(rs[len(rs)-summaryMaxChars:])
	}
	return s
}

// minutesPromptLocked は議事録（要約）を作るプロンプトを返す。
// 今の会話で作った議事録があれば、その内容と、それより後の発言から作る（差分）。
// 前回の議事録がない・読めない・その後の発言がない場合は、今までどおり会話全体から作る。空なら要約する発言がない
func (r *Room) minutesPromptLocked() (prompt, mode string) {
	if prev := r.lastMinutes; prev != nil && prev.upto <= len(r.messages) {
		delta := transcriptOf(r.messages[prev.upto:])
		b, err := os.ReadFile(prev.path)
		switch {
		case err != nil:
			r.log.Warn("minutes.delta", "path", prev.path, "error", err.Error())
		case strings.TrimSpace(string(b)) != "" && strings.TrimSpace(delta) != "":
			return summaryDeltaInstruction + strings.TrimSpace(string(b)) + summaryDeltaSeparator + delta, "delta"
		}
	}
	transcript := r.transcriptLocked()
	if strings.TrimSpace(transcript) == "" {
		return "", "full"
	}
	return summaryInstruction + transcript, "full"
}

// SummarizeAndReset は実行中の処理を止め、会話の要約を作ってから新しい会話を始める。
// 要約の作成には時間がかかるため裏で行い、結果（または失敗）は系统メッセージで知らせる。
func (r *Room) SummarizeAndReset() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.summarizing {
		return ErrSummarizing
	}
	a := r.summarizerLocked()
	if a == nil {
		return ErrNoAgents
	}
	prompt, mode := r.minutesPromptLocked()
	if prompt == "" {
		return ErrNothingToSumm
	}
	r.stopLocked()
	r.summarizing = true
	oldLog := filepath.Base(r.logFile)
	gen, workdir, modelSel := r.gen, r.workdir, a.modelSel
	r.postLocked("system", fmt.Sprintf("%s 正在总结对话。完成后开始新对话。", a.Name), "system")
	r.log.Info("conversation.summarize.start", "agent", a.ID, "log_file", oldLog, "messages", len(r.messages), "mode", mode)
	ctx, cancel := context.WithCancel(context.Background())
	r.summaryCancel = cancel
	ctx = withPermission(ctx, a.permission)
	ctx = withTurnTimeout(ctx, r.turnTimeoutForLocked(a))
	go r.finishSummary(ctx, cancel, a, prompt, oldLog, gen, len(r.messages), workdir, modelSel)
	return nil
}

func (r *Room) finishSummary(ctx context.Context, cancel context.CancelFunc, a *Agent, prompt, oldLog string, gen, upto int, workdir, modelSel string) {
	defer cancel()
	start := time.Now()
	// 要約は既存の会話を引き継がず、新しいセッションで作る
	res, err := a.Adapter.Run(ctx, prompt, "", modelSel, workdir)

	r.mu.Lock()
	defer r.mu.Unlock()
	r.summarizing = false
	r.summaryCancel = nil
	a.usage.add(res.Usage)
	r.saveUsageLocked()
	log := r.log.With("agent", a.ID, "log_file", oldLog, "latency_ms", time.Since(start).Milliseconds())
	if gen != r.gen { // 要約中に「新しい会話」などで会話が変わった
		log.Info("conversation.summarize.end", "discarded", "reset")
		return
	}
	if ctx.Err() != nil {
		log.Info("conversation.summarize.end", "discarded", "canceled")
		r.pushStatusLocked()
		return
	}
	// 要約開始後に届いた発言はプロンプトに含まれていない。会話をリセットすると
	// 追加依頼やコマンド結果が失われるので、現在の会話を維持する。
	if len(r.messages) != upto {
		log.Info("conversation.summarize.end", "discarded", "new_messages")
		r.postLocked("system", "总结期间收到新发言，已取消切换对话。新增发言保留。", "system")
		r.pushStatusLocked()
		return
	}
	summary := strings.TrimSpace(cleanReply(a, res.Text))
	if err == nil && summary == "" {
		err = errors.New("总结为空")
	}
	if err != nil {
		log.Error("conversation.summarize.end", "error", err.Error())
		r.postLocked("system", fmt.Sprintf("总结对话失败（%s）。对话可照常继续。", err.Error()), "system")
		r.pushStatusLocked()
		return
	}
	log.Info("conversation.summarize.end", "summary_chars", len([]rune(summary)))
	r.resetLocked()
	r.postProfileLocked()
	r.postLocked("system", fmt.Sprintf("这是上一段对话（%s）的总结（生成: %s）。请据此继续。\n\n%s", oldLog, a.Name, summary), "system")
}
