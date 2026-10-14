package main

// 作者の環境のプロフィールと、この会話でのルールを、会話の先頭に系统の発言として載せる（修正案 7.3）。
// 起動時と、新しい会話を始めたとき（「新しい会話」「要約して新しい会話」、作業ディレクトリの変更）に投稿する。
// 環境変数の値は載せない（キーや秘密が混ざるため）。載せるのは OS とシェルの有無・バージョンだけ。

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// defaultRules はこの会話でのルール（rules.md がないときに使う。@claude2 の文面、#1592。1 は宛先の仕様に合わせて 2026-10-03 に変更）
const defaultRules = `1. 没有指定收件人的问题，所有人可从各自立场回答。答案会与他人相同时回复 [pass]。被点名的问题由被点名者回答。
2. 工作在主持分配后开始。主动请缨后等主持答复。
3. 请作者运行的命令只由主持给出。其他人向主持提议。
4. 这类命令写在带语言标注的代码块（powershell / cmd / bash）里。作者可用［运行］按钮执行，所以不要创建脚本文件。
5. 命令按上面「环境」里列出的 shell 来写。不要混用 cmd 和 PowerShell 语法（例如 cmd 里不能用 $env:TEMP）。
6. 生产变更、IAM、删除由作者执行。代理不改写自己的权限规则，也不绕过安全确认。不要编辑规则或权限设置（.claude/、CLAUDE.md、GEMINI.md、rules.md 等）。
7. 秘密信息（密钥、token、密码）不写进聊天。不要读取或编辑 {{ai_agent_room_dir}}。
8. 使用共享资源（CDP 端口、项目的 logs/ 等）前确认没有人在用。测试日志输出到临时文件夹。
9. 报告完成时，把已确认的内容（build、test、页面）和未确认的分开写。`

// defaultRulesEn は defaultRules の英語版（言語の設定が英語で、rules.en.md も rules.md もないときに使う）
const defaultRulesEn = `1. Anyone may answer a question with no addressee from their own point of view. If your answer would repeat someone else's, reply [pass]. A question addressed to someone is answered by that person.
2. Start work only after the leader assigns it. If you volunteer, wait for the leader's reply.
3. Only the leader gives commands for the human to run. Others suggest them to the leader.
4. Write those commands in code blocks with a language (powershell / cmd / bash). The human can run them with the [Run] button, so do not create script files.
5. Write commands for a shell listed under "Environment" above. Do not mix cmd and PowerShell syntax (for example, $env:TEMP does not work in cmd).
6. The human makes production changes, IAM changes and deletions. Agents do not rewrite their own permission rules or bypass safety checks. Do not edit rules or permission settings (.claude/, CLAUDE.md, GEMINI.md, rules.md and so on).
7. Do not write secrets (keys, tokens, passwords) in the chat. Do not read or edit {{ai_agent_room_dir}}.
8. Before using something shared (CDP ports, the project's logs/ and so on), check that it is not in use. Write test logs to a temporary folder.
9. When reporting that you are done, separate what you checked (build, test, screen) from what you did not check.`

// rulesFileName はルールを差し替えるファイル。設定フォルダ（AI Agent Room 文件夹の config）に置く（作者が再ビルドせずに直せる）。
// 文面の {{ai_agent_room_dir}} は、投稿するときにこの環境の AI Agent Room 文件夹に置き換える。
// 言語の設定が英語のときは rulesFileNameEn を先に探し、なければ rulesFileName を使う（作者が書いたルールを言語の設定で無視しない）
const (
	rulesFileName   = "rules.md"
	rulesFileNameEn = "rules.en.md"
)

// exeDir は実行ファイルのフォルダ（設定ファイルの旧い置き場所）
func exeDir() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	return filepath.Dir(exe)
}

// shellProbe は検出するシェル1つ
type shellProbe struct {
	name string   // 表示名
	exe  []string // 実行ファイルの候補（最初に見つかったものを使う）
	args []string // バージョンを出す引数
}

// envLookPath / envRunVersion はテストで差し替える
var (
	envLookPath   = exec.LookPath
	envRunVersion = func(exe string, args []string) (string, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, exe, args...)
		hideWindow(cmd)
		out, err := cmd.Output()
		return strings.TrimSpace(string(out)), err
	}
)

func shellProbes() []shellProbe {
	if runtime.GOOS != "windows" {
		return []shellProbe{
			{"bash", []string{"bash"}, []string{"--version"}},
			{"pwsh", []string{"pwsh"}, []string{"-NoProfile", "-Command", "$PSVersionTable.PSVersion.ToString()"}},
		}
	}
	gitBash := []string{}
	for _, d := range []string{os.Getenv("ProgramFiles"), os.Getenv("ProgramW6432")} {
		if d != "" {
			gitBash = append(gitBash, filepath.Join(d, "Git", "bin", "bash.exe"))
		}
	}
	return []shellProbe{
		{"PowerShell 7 (pwsh)", []string{"pwsh"}, []string{"-NoProfile", "-NonInteractive", "-Command", "$PSVersionTable.PSVersion.ToString()"}},
		{"Windows PowerShell", []string{"powershell"}, []string{"-NoProfile", "-NonInteractive", "-Command", "$PSVersionTable.PSVersion.ToString()"}},
		{"cmd", []string{"cmd"}, []string{"/d", "/c", "ver"}},
		{"Git Bash", gitBash, []string{"--version"}},
	}
}

// envInfo は起動時に調べた環境。文面は投稿するときに、言語の設定に合わせて text で作る
type envInfo struct {
	OS     string // GOOS/GOARCH
	Shells []shellFound
}

// shellFound はシェル1つを調べた結果
type shellFound struct {
	Name      string
	Found     bool
	VersionNG bool   // 見つかったが、バージョンを取得できなかった
	Version   string // バージョンの最初の空でない行（出力が空なら空）
}

// text は環境の部分の文面を lang（"ja" / "en"）で返す
func (e *envInfo) text(lang string) string {
	en := lang == langEn
	var b strings.Builder
	fmt.Fprintf(&b, "- OS: %s", e.OS)
	b.WriteString(pick(en, "\n- Shells:", "\n- Shell:"))
	for _, s := range e.Shells {
		v := s.Version
		switch {
		case !s.Found:
			v = pick(en, "not found", "未找到")
		case s.VersionNG:
			v = pick(en, "available (version unknown)", "有（无法取得版本）")
		case v == "":
			v = pick(en, "available", "有")
		}
		fmt.Fprintf(&b, "\n  - %s: %s", s.Name, v)
	}
	return b.String()
}

// pick は en なら e を、そうでなければ j を返す
func pick(en bool, e, j string) string {
	if en {
		return e
	}
	return j
}

// detectEnvironment は OS と使えるシェルを調べる。
// 起動時に1回だけ呼ぶ（pwsh の起動に1秒ほどかかるため）。シェルが見つからなくても失敗しない
func detectEnvironment() *envInfo {
	e := &envInfo{OS: runtime.GOOS + "/" + runtime.GOARCH}
	for _, p := range shellProbes() {
		e.Shells = append(e.Shells, probeShell(p))
	}
	return e
}

func probeShell(p shellProbe) shellFound {
	for _, c := range p.exe {
		exe, err := envLookPath(c) // フルパス（Git Bash）はそのファイルがあるかを確かめる
		if err != nil {
			continue
		}
		out, err := envRunVersion(exe, p.args)
		if err != nil {
			return shellFound{Name: p.name, Found: true, VersionNG: true}
		}
		// 最初の空でない行だけを使う（cmd の ver は先頭が空行、bash --version は複数行）
		for _, line := range strings.Split(out, "\n") {
			if line = strings.TrimSpace(line); line != "" {
				return shellFound{Name: p.name, Found: true, Version: line}
			}
		}
		return shellFound{Name: p.name, Found: true}
	}
	return shellFound{Name: p.name}
}

// SetEnvProfile は起動時に調べた環境と設定フォルダ（configDir。旧い置き場所 legacyDir も読む）を設定し、
// 今の会話にプロフィールを投稿する
func (r *Room) SetEnvProfile(env *envInfo, configDir, legacyDir string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.envProfile, r.configDir, r.legacyConfigDir = env, configDir, legacyDir
	r.postProfileLocked()
}

// ConfigDir は作者が編集する設定のフォルダを返す
func (r *Room) ConfigDir() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.configDir
}

// postProfileLocked は環境のプロフィールとルールを投稿する（環境を調べる前は何もしない）。
// ルールの5番が「上の『環境』」を指すので、環境を先に並べる。文面は言語の設定（r.lang）に合わせる
func (r *Room) postProfileLocked() {
	if r.envProfile == nil {
		return
	}
	en := r.lang == langEn // zh 用中文框架（文案已译为中文），但规则文件沿用 langLocked（rules.en.md）
	var legacy []string // 旧い置き場所から読んだファイル（作者に移してもらう）
	rules, _, p, old := r.resolveRulesLocked(r.langLocked())
	if old {
		legacy = append(legacy, p)
	}
	// ルールの文面の {{ai_agent_room_dir}} は、この環境の AI Agent Room 文件夹（エージェントに読ませない場所）に置き換える
	rules = strings.ReplaceAll(rules, appDirPlaceholder, firstNonEmpty(r.appDirLocked(), pick(en, "the AI Agent Room folder", "AI Agent Room 文件夹")))
	caps, oldCaps := r.capabilitiesTextLocked(en)
	if oldCaps != "" {
		legacy = append(legacy, oldCaps)
	}
	// 作者への確認・依頼に @human を付けてもらうため、参加者として作者も載せる（画面の「あなたへ」は @human 宛ての発言を集める）
	format := "【环境与本对话的规则】（AI Agent Room 在启动和新对话开始时投递）\n\n■ 环境\n%s\n- 工作目录: %s\n\n" +
		"■ 参与者\n- @human（作者）: 提出要求、确认、裁定，并用［运行］执行命令。需要作者确认、裁定或做事时，在发言里加 @human（会汇集到页面的「给你」）\n- 代理: 见下面的「■ 代理」\n\n" +
		"■ 代理（能做什么。由作者在配置文件夹的 %s 中声明）\n%s\n\n■ 本对话的规则\n%s"
	if en {
		format = "[Environment and rules for this chat] (posted by AI Agent Room at startup and when a new chat starts)\n\n■ Environment\n%s\n- Working directory: %s\n\n" +
			"■ Participants\n- @human (the human): makes requests, checks and decides, and runs commands with [Run]. When you need the human to check, decide or do something, put @human in your message (it is collected under \"For you\" on the screen)\n- Agents: as listed under \"■ Agents\" below\n\n" +
			"■ Agents (what each may do, declared by the human in %s in the config folder)\n%s\n\n■ Rules for this chat\n%s"
	}
	text := fmt.Sprintf(format, r.envProfile.text(pick(en, langEn, langJa)), r.workdir, capabilitiesFileName, caps, rules)
	if len(legacy) > 0 {
		// 旧い置き場所はエージェントが書き換えられる場所なので、作者に移してもらう
		text += fmt.Sprintf(pick(en, "\n\n[Note] These files were read from the old location. Move them to the config folder (%s): %s",
			"\n\n【注意】以下文件是从旧位置读取的，请移到配置文件夹（%s）: %s"),
			r.configDir, strings.Join(legacy, pick(en, ", ", "、")))
		r.log.Warn("config.legacy", "files", strings.Join(legacy, ","))
	}
	r.postLocked("system", text, "system")
}

// resolveRulesLocked は lang の投稿に使うルールの文面と、その出どころ（使うファイル名。既定なら rulesDefault）を返す。
// 英語は rules.en.md、なければ rules.md、どちらもなければ既定の英語のルール。空のファイルは既定のルールにする。
// path・legacy は読んだファイルと、それが旧い置き場所か
func (r *Room) resolveRulesLocked(lang string) (text, inUse, path string, legacy bool) {
	names := []string{rulesFileName}
	if lang == langEn {
		names = []string{rulesFileNameEn, rulesFileName}
	}
	for _, name := range names {
		p, old := resolveConfigFile(r.configDir, r.legacyConfigDir, name)
		if p == "" {
			continue
		}
		if b, err := os.ReadFile(p); err == nil && strings.TrimSpace(string(b)) != "" {
			return strings.TrimSpace(string(b)), name, p, old
		}
		return defaultRulesFor(lang), rulesDefault, p, old
	}
	return defaultRulesFor(lang), rulesDefault, "", false
}

// rulesDefault は、ファイルではなく既定のルールを使っていることを表す
const rulesDefault = "default"

// defaultRulesFor は lang の既定のルール
func defaultRulesFor(lang string) string {
	return pick(lang == langEn, defaultRulesEn, defaultRules)
}

// appDirPlaceholder はルールの文面で、AI Agent Room 文件夹（設定フォルダの親）に置き換える文字列
const appDirPlaceholder = "{{ai_agent_room_dir}}"

// appDirLocked は AI Agent Room 文件夹（認証トークンと設定の置き場所）を返す
func (r *Room) appDirLocked() string {
	if r.configDir == "" {
		return ""
	}
	return filepath.Dir(r.configDir)
}

// ---- エージェントごとのできること（修正案 7.3） -------------------------------------------------

// capabilitiesFileName は、エージェントごとにできることを作者が宣言するファイル。rules.md と同じフォルダに置く。
// 例: {"claude": {"write": true, "exec": true, "prod": false}, "agy": {"write": false}}
// 書いていない項目・エージェントは「不明」と出す（AI Agent Room には実際の権限を調べる方法がないため）
const capabilitiesFileName = "capabilities.json"

// AgentCapabilities はエージェント1人のできること。nil は宣言なし（不明）
type AgentCapabilities struct {
	Write *bool `json:"write"` // ファイルを書き込めるか
	Exec  *bool `json:"exec"`  // コマンドを実行できるか
	Prod  *bool `json:"prod"`  // 本番の変更（配備・IAM・削除）をしてよいか
}

func capText(v *bool, en bool) string {
	switch {
	case v == nil:
		return pick(en, "unknown", "未知")
	case *v:
		return pick(en, "yes", "可")
	default:
		return pick(en, "no", "不可")
	}
}

// capabilitiesTextLocked は参加中のエージェントのできることを1人1行で返す（en なら英語）。旧い置き場所から読んだらそのパスも返す
func (r *Room) capabilitiesTextLocked(en bool) (text, legacyPath string) {
	caps := map[string]AgentCapabilities{}
	if p, old := resolveConfigFile(r.configDir, r.legacyConfigDir, capabilitiesFileName); p != "" {
		if old {
			legacyPath = p
		}
		if b, err := os.ReadFile(p); err == nil {
			if err := json.Unmarshal(b, &caps); err != nil {
				r.log.Warn("capabilities.load", "error", err.Error())
			}
		}
	}
	format := pick(en, "- @%s (%s): write %s / run commands %s / production %s", "- @%s（%s）: 写入 %s / 运行命令 %s / 生产操作 %s")
	var lines []string
	for _, a := range r.agents {
		c := caps[a.ID]
		lines = append(lines, fmt.Sprintf(format, a.ID, a.Name, capText(c.Write, en), capText(c.Exec, en), capText(c.Prod, en)))
	}
	return strings.Join(lines, "\n"), legacyPath
}
