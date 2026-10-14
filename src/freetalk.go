package main

// フリートーク: 全エージェントが常に待機し、新しい発言を見て話したいときに自由に発言する。
//
// CLI エージェントは話しかけられない限り発言できないため、エージェントごとに待機ループを動かし、
// 他の参加者の新しい発言があれば「話したいことがあれば発言、なければ [pass]」と尋ねる。
//   - 会話が delay だけ途切れてから考え始める（作者が割り込む余地を残す）
//   - パスは表示しない。全員がパスすると会話は自然に止まり、次の発言を待つ
//   - 作者の発言1回あたりのエージェントの発言数は maxHops まで（暴走防止。0 は無制限）

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// FreeTalk はフリートークの状態
type FreeTalk struct {
	Topic     string `json:"topic"`
	StartedAt int64  `json:"started_at"` // UnixMilli

	limitNotified bool // 発言数の上限に達したことを通知済みか
	ctx           context.Context
	cancel        context.CancelFunc
}

const freeTalkMaxFailures = 3 // 連続で失敗したエージェントはフリートークから外す

// StartFreeTalk はフリートークを開始する。topic が空でなければ作者の発言として投稿する。
func (r *Room) StartFreeTalk(topic string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.disc != nil || r.free != nil {
		return ErrDiscussionBusy
	}
	ids := r.activeIDs("")
	if len(ids) == 0 {
		return ErrNoAgents
	}
	r.queue = nil // チャットの発言待ちはフリートークに置き換える
	ctx, cancel := context.WithCancel(context.Background())
	ft := &FreeTalk{Topic: topic, StartedAt: time.Now().UnixMilli(), ctx: ctx, cancel: cancel}
	r.free = ft
	r.hops = 0
	if topic != "" {
		r.postLocked("human", topic, "chat")
		r.checkRotateLocked()
	}
	limit := fmt.Sprintf("作者每条发言最多 %d 次", r.maxHops)
	if r.maxHops == 0 {
		limit = "不限"
	}
	r.postLocked("system", fmt.Sprintf("自由谈开始: 所有人看到新发言后，想说就自由发言。"+
		"代理发言%s（可在上限设置中更改）。按「停止」结束。", limit), "system")
	r.log.Info("free_talk.start", "agents", len(ids))
	for _, id := range ids {
		a := r.agent(id)
		if a.state == "idle" {
			a.state = "listening"
		}
		go r.freeLoop(ft, a)
	}
	r.pushStatusLocked()
	return nil
}

func (r *Room) endFreeTalkLocked(reason string) {
	ft := r.free
	if ft == nil {
		return
	}
	ft.cancel()
	r.free = nil
	for _, a := range r.agents {
		if a.state == "listening" || a.state == "waiting" {
			a.state = "idle"
		}
		a.waitUntil = time.Time{}
	}
	r.log.Info("free_talk.end", "reason", reason)
	r.cond.Broadcast() // 待機中のループを終了させる
}

// hasNewChatLocked は a がまだ受け取っていない、a 宛ての他の参加者の発言があるかを返す。
// 系统メッセージだけでは発言のきっかけにしない。コマンドの実行結果は、呼び出す相手（進行役）だけのきっかけにする
func (r *Room) hasNewChatLocked(a *Agent) bool {
	for _, m := range r.messages[a.cursor:] {
		if m.From != a.ID && m.Kind == "chat" && r.freeTalkAddressedLocked(m, a.ID) {
			return true
		}
		if m.Kind == "command_result" && r.commandNotifyTargetLocked(m) == a.ID {
			return true
		}
	}
	return false
}

// freeTalkAddressedLocked は、フリートークの発言 m で id のエージェントを起動するかを返す。
// 作者・進行役・メンバーの区別なく、宛先（@ID・@human）がなければ全員（@all と同じ）、あれば宛先の人だけを起動する。
// 宛先にしなかったエージェントも、次に起動したときに新着としてこの発言を受け取る
func (r *Room) freeTalkAddressedLocked(m Message, id string) bool {
	ids, all := r.mentions(m.Text, m.From)
	if all || contains(ids, id) {
		return true
	}
	return len(ids) == 0 && !mentionsHuman(m.Text)
}

// mentionsHuman は text が @human で作者に呼びかけているかを返す
func mentionsHuman(text string) bool {
	for _, m := range mentionRe.FindAllStringSubmatch(text, -1) {
		if strings.EqualFold(m[1], "human") {
			return true
		}
	}
	return false
}

// freeLoop はフリートーク中、エージェント a の発言のきっかけを待ち続ける。
func (r *Room) freeLoop(ft *FreeTalk, a *Agent) {
	r.mu.Lock()
	defer r.mu.Unlock()
	failures := 0
	for r.free == ft {
		if a.paused || a.interactive { // 一時停止・対話モードになったら抜ける（戻ると SetPaused / waitInteractive が聞き直させる）
			if a.state == "listening" || a.state == "waiting" {
				a.state, a.waitUntil = "idle", time.Time{}
			}
			r.pushStatusLocked()
			return
		}
		// 新着がない / 考え中（チャットのターンが残っている場合など）なら次の発言まで待つ
		if a.state == "thinking" || !r.hasNewChatLocked(a) {
			r.cond.Wait()
			continue
		}
		if hopLimitReached(r.hops, r.maxHops) {
			if !ft.limitNotified {
				ft.limitNotified = true
				r.postLocked("system", fmt.Sprintf("代理发言已达上限（%d 次）。作者发言后恢复。", r.maxHops), "system")
				r.pushStatusLocked()
			}
			r.cond.Wait()
			continue
		}
		// 会話が delay だけ途切れるまで待つ。待っている間に新しい発言があれば待ち直す
		if wait := time.Until(r.lastPost.Add(r.delay)); wait > 0 {
			a.state, a.waitUntil = "waiting", time.Now().Add(wait)
			r.pushStatusLocked()
			r.mu.Unlock()
			select {
			case <-time.After(wait):
			case <-ft.ctx.Done():
			}
			r.mu.Lock()
			if r.free == ft {
				a.state, a.waitUntil = "listening", time.Time{}
			}
			continue
		}

		r.mu.Unlock()
		outcome := r.runTurn(ft.ctx, a)
		r.mu.Lock()

		if outcome != outcomeFailed {
			failures = 0
			continue
		}
		failures++
		if failures >= freeTalkMaxFailures && r.free == ft {
			a.state = "idle"
			r.postLocked("system", fmt.Sprintf("%s 连续失败 %d 次，已退出自由谈。", a.Name, failures), "system")
			r.log.Error("free_talk.agent_removed", "agent", a.ID, "failures", failures)
			r.pushStatusLocked()
			return
		}
	}
}
