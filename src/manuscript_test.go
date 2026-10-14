package main

import (
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newMsRoom(t *testing.T) (*Room, string) {
	t.Helper()
	logDir := t.TempDir()
	a := &Agent{ID: "claude", Type: "claude", Name: "Claude Code", Aliases: []string{"claude"}, Adapter: fakeAdapter{}}
	r := NewRoom([]*Agent{a}, t.TempDir(), logDir, 10, time.Second, slog.New(slog.NewTextHandler(io.Discard, nil)))
	return r, logDir
}

// 候选从 ```candidate 块取出；接受时校验基准；作者改稿后旧候选过期；撤销后基准正文恢复、候选可再接受
func TestManuscriptCandidateLifecycle(t *testing.T) {
	r, logDir := newMsRoom(t)
	v1, err := r.SetManuscript("In this retrospective study, X was associated with Y.", "", 0)
	if err != nil || v1.ID != 1 {
		t.Fatalf("SetManuscript: %v %+v", err, v1)
	}
	if _, err := r.SetManuscript("x", "", 0); !errors.Is(err, ErrMsStale) {
		t.Fatalf("stale base should be refused, got %v", err)
	}
	var b strings.Builder
	r.mu.Lock()
	ver, dec := r.writeManuscriptPrompt(&b)
	r.mu.Unlock()
	if ver != 1 || dec != 0 || !strings.Contains(b.String(), "稿件 v1") || !strings.Contains(b.String(), "```candidate") {
		t.Fatalf("prompt snapshot wrong: ver=%d dec=%d\n%s", ver, dec, b.String())
	}
	r.mu.Lock()
	m := r.appendMessageLocked(Message{From: "claude", Kind: "chat", Model: "m", Turn: "turn-1", Text: "理由。\n```candidate\nIn this retrospective study, X was associated with Y.\n```\n```bash\necho no\n```\n重复：\n```candidate\nIn this retrospective study, X was associated with Y.\n```"})
	n := r.addCandidatesLocked(m, "turn-1", 1, 0)
	r.mu.Unlock()
	if n != 1 { // bash 块不算；同一条回复里重复的候选块只登记一次
		t.Fatalf("want 1 candidate, got %d", n)
	}
	snap := r.ManuscriptSnapshot()
	if len(snap.Candidates) != 1 || snap.Candidates[0].Stale || snap.Candidates[0].MissedDecision != 0 {
		t.Fatalf("candidate state: %+v", snap.Candidates)
	}
	// 作者改稿 → 候选过期，接受被拒
	if _, err := r.SetManuscript("Edited by author.", "", 1); err != nil {
		t.Fatal(err)
	}
	if !r.ManuscriptSnapshot().Candidates[0].Stale {
		t.Fatal("candidate should be stale after author edit")
	}
	if _, err := r.AcceptCandidate(1, 2, ""); !errors.Is(err, ErrMsStale) {
		t.Fatalf("accept of stale candidate should fail, got %v", err)
	}
	// 撤销 → 正文回到 v1，候选不再过期；接受时 base 仍须是当前版本 ID（3）
	if _, err := r.UndoManuscript(); err != nil {
		t.Fatal(err)
	}
	if r.ManuscriptSnapshot().Candidates[0].Stale {
		t.Fatal("candidate should be fresh after undo")
	}
	if _, err := r.AcceptCandidate(1, 1, ""); !errors.Is(err, ErrMsStale) {
		t.Fatalf("accept with old base id should fail, got %v", err)
	}
	v, err := r.AcceptCandidate(1, 3, "In this retrospective study, X was associated with Y (edited).")
	if err != nil || v.ID != 4 || v.Note != "作者微调" {
		t.Fatalf("accept: %v %+v", err, v)
	}
	if _, err := r.AcceptCandidate(1, 4, ""); !errors.Is(err, ErrMsNotPending) {
		t.Fatalf("double accept should fail, got %v", err)
	}
	// 重启后恢复
	r2 := NewRoom([]*Agent{{ID: "claude", Type: "claude", Name: "c", Adapter: fakeAdapter{}}}, t.TempDir(), logDir, 10, time.Second, slog.New(slog.NewTextHandler(io.Discard, nil)))
	s2 := r2.ManuscriptSnapshot()
	if len(s2.Versions) != 4 || s2.Candidates[0].Status != "accepted" {
		t.Fatalf("restore: %d versions, cand %+v", len(s2.Versions), s2.Candidates)
	}
}

// 裁定：之前快照产生的候选标为「未见裁定」且不能直接接受；清单文件记录快照
func TestManuscriptDecisionAndManifest(t *testing.T) {
	r, logDir := newMsRoom(t)
	r.agents[0].paused = true // 不起真正的轮次（后台写文件会撞上 TempDir 清理）
	if _, err := r.SetManuscript("S1. S2.", "", 0); err != nil {
		t.Fatal(err)
	}
	r.mu.Lock()
	m := r.appendMessageLocked(Message{From: "claude", Kind: "chat", Text: "```candidate\nS1 new. S2 new.\n```"})
	r.addCandidatesLocked(m, "turn-1", 1, 0)
	r.mu.Unlock()
	if _, err := r.PostHumanDecision("保留第一句，重写第二句，不增加限制段。", 0, true); err != nil {
		t.Fatal(err)
	}
	snap := r.ManuscriptSnapshot()
	if len(snap.Decisions) != 1 || snap.Candidates[0].MissedDecision != 1 {
		t.Fatalf("decision not applied: %+v %+v", snap.Decisions, snap.Candidates)
	}
	if _, err := r.AcceptCandidate(1, 1, ""); !errors.Is(err, ErrMsMissedDecision) {
		t.Fatalf("candidate that missed a decision must not be accepted, got %v", err)
	}
	r.writeManifest(turnManifest{Turn: "turn-9", Agent: "claude", MsVersion: 1, Decisions: 1, DeltaIDs: []int{2}}, "PROMPT BODY")
	mf, ok := r.Manifest("turn-9")
	if !ok || mf.MsVersion != 1 || mf.Decisions != 1 || mf.Prompt != "PROMPT BODY" {
		t.Fatalf("manifest: %v %+v", ok, mf)
	}
	if _, err := os.Stat(filepath.Join(logDir, "prompts", "turn-9-claude.txt")); err != nil {
		t.Fatal(err)
	}
}
