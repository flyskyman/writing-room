package main

// エージェントの一時停止。一時停止中のエージェントは宛先・フリートーク・ディスカッション・進行役・要約の対象から外す。
// 利用枠の上限エラー（usage limit など）で失敗したら自动一時停止にし、戻すのは作者が画面のモデル選択で行う。
// 一時停止の状態は logs/session.json に保持し、再起動後も続く。

import (
	"fmt"
	"regexp"
)

// quotaErrorRe は各 CLI の利用枠の上限エラーに一致する
// 例: Codex「You've hit your usage limit.」、Claude Code「Claude AI usage limit reached」、Gemini 系「RESOURCE_EXHAUSTED」
var quotaErrorRe = regexp.MustCompile(`(?i)usage limit|hit your (usage |session |weekly )?limit|rate limit reached|quota (exceeded|exhausted)|resource[_ ]exhausted|insufficient (credits|quota)|out of credits`)

// isQuotaError は、CLI がエラーとして返した文面が利用枠の上限によるものかを返す（呼び出し元は cliFailure / procFailure）
func isQuotaError(msg string) bool { return quotaErrorRe.MatchString(msg) }

// active は、エージェントが会話に参加できる状態か（インストール済みで一時停止中でない）を返す
// 対話モードの窓を開いている間（案9。interactive.go）も参加させない
func (a *Agent) active() bool { return a.state != "unavailable" && !a.paused && !a.interactive }

// SetPaused は作者の操作でエージェントを一時停止・再開する
func (r *Room) SetPaused(id string, paused bool) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	a := r.agent(id)
	if a == nil {
		return ErrAgentNotFound
	}
	if a.paused == paused {
		return nil
	}
	if paused {
		r.pauseLocked(a, "human")
		return nil
	}
	a.paused = false
	r.saveSessionLocked()
	r.log.Info("agent.resume", "agent", a.ID)
	go r.refreshQuota(a, true) // 一時停止中は取得しないので、再開したら取り直す
	r.postLocked("system", fmt.Sprintf("已解除 %s 的暂停。", a.Name), "system")
	// フリートーク中なら、再開したエージェントも聞き始める
	if r.free != nil && a.state == "idle" {
		a.state = "listening"
		go r.freeLoop(r.free, a)
	}
	r.pushStatusLocked()
	return nil
}

// pauseLocked はエージェントを一時停止にする。by は human（画面の操作）か quota（利用枠の上限エラー）
func (r *Room) pauseLocked(a *Agent, by string) {
	a.paused = true
	note := ""
	if r.leader == a.ID { // 一時停止中のエージェントは進行役にできない
		r.leader, note = "", "同时取消了主持指定。"
	}
	r.saveSessionLocked()
	r.log.Info("agent.pause", "agent", a.ID, "by", by)
	if by == "quota" {
		r.postLocked("system", fmt.Sprintf("%s 已达额度上限，已暂停。可用后请在模型选择里恢复。%s", a.Name, note), "system")
	} else {
		r.postLocked("system", fmt.Sprintf("已暂停 %s。%s", a.Name, note), "system")
	}
	r.pushStatusLocked()
}
