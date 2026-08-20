package asabaguess

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/FloatTech/zbputils/control"
	zero "github.com/wdvxdr1123/ZeroBot"
	"github.com/wdvxdr1123/ZeroBot/message"
)

// registerCommands 注册全部指令.
func registerCommands(engine *control.Engine) {
	// ---------- 公用 ----------
	engine.OnFullMatch(prefix+"猜歌", zero.OnlyGroup).SetBlock(true).
		Handle(func(ctx *zero.Ctx) { beginSignup(ctx) })

	engine.OnFullMatch(prefix+"报名", zero.OnlyGroup).SetBlock(true).
		Handle(func(ctx *zero.Ctx) { joinSignup(ctx) })

	engine.OnFullMatch(prefix+"取消猜歌", zero.OnlyGroup).SetBlock(true).
		Handle(func(ctx *zero.Ctx) { cancelGame(ctx) })

	engine.OnFullMatch(prefix+"战绩", zero.OnlyGroup).SetBlock(true).
		Handle(showHistory)

	engine.OnFullMatch(prefix+"题库", zero.OnlyGroup).SetBlock(true).
		Handle(showPool)

	engine.OnFullMatch(prefix+"配置", zero.OnlyGroup).SetBlock(true).
		Handle(showConfig)

	// ---------- 管理员: 修改配置 ----------
	engine.OnPrefix(prefix+"设置", zero.AdminPermission).SetBlock(true).
		Handle(setConfig)

	// ---------- 主人: 资源包导入导出 ----------
	engine.OnPrefix(prefix+"导入歌包", zero.SuperUserPermission).SetBlock(true).
		Handle(importPack)

	engine.OnPrefix(prefix+"导出歌包", zero.SuperUserPermission).SetBlock(true).
		Handle(exportPack)
}

// showPool 展示题库概况.
func showPool(ctx *zero.Ctx) {
	qs, err := loadQuestions()
	if err != nil || len(qs) == 0 {
		ctx.SendChain(message.Text("题库为空, 请主人通过「", prefix, "导入歌包」导入资源包~"))
		return
	}
	stat := make(map[string]int)
	for _, q := range qs {
		stat[q.Difficulty]++
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("题库共 %d 题:\n", len(qs)))
	for _, d := range cfg.sortedDifficulties() {
		if n, ok := stat[d]; ok && n > 0 {
			sb.WriteString(fmt.Sprintf("  %s %s: %d 题\n", d, cfg.difficultyName(d), n))
		}
	}
	ctx.SendChain(message.Text(sb.String()))
}

// showConfig 展示配置.
func showConfig(ctx *zero.Ctx) {
	data, _ := json.MarshalIndent(&cfg, "", "  ")
	ctx.SendChain(message.Text("当前玩法配置:\n", string(data)))
}

// setConfig 修改配置. 用法: 浅羽设置 <键> <值>
func setConfig(ctx *zero.Ctx) {
	option := strings.TrimSpace(ctx.State["args"].(string))
	if option == "" {
		ctx.SendChain(message.Text("用法: ", prefix, "设置 <配置键> <值>\n发送「", prefix, "配置」查看全部可配置项"))
		return
	}
	key, val := option, ""
	if idx := strings.IndexAny(option, " ="); idx != -1 {
		key = strings.TrimSpace(option[:idx])
		val = strings.TrimSpace(option[idx+1:])
	}
	err := applyConfigKey(strings.ToLower(key), val)
	if err != nil {
		ctx.SendChain(message.Text(serviceErr, err))
		return
	}
	saveConfig()
	ctx.SendChain(message.Text("设置成功! 当前配置:\n", configSummary()))
}

// configSummary 配置摘要.
func configSummary() string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("报名时间 %ds / 答题时间 %ds\n", cfg.SignupTimeout, cfg.AnswerTimeout))
	sb.WriteString(fmt.Sprintf("人数范围 %d~%d, 选项数 %d(%s)\n", cfg.MinPlayers, cfg.MaxPlayers, cfg.OptionsCount, cfg.AnswerStyle))
	var rounds []string
	for _, d := range cfg.sortedDifficulties() {
		rounds = append(rounds, fmt.Sprintf("%s:%d首", d, cfg.DifficultyRounds[d]))
	}
	sb.WriteString("难度与曲目数: " + strings.Join(rounds, " "))
	sb.WriteString(fmt.Sprintf("\n可出题类型: %s", strings.Join(cfg.QuestionTypes, "/")))
	sb.WriteString(fmt.Sprintf("\n允许平局: %v", cfg.AllowTie))
	sb.WriteString(fmt.Sprintf("\n超时未答淘汰: %v", cfg.EliminateOnMiss))
	return sb.String()
}

// applyConfigKey 应用单个配置项.
func applyConfigKey(key, val string) error {
	switch key {
	case "signup_timeout":
		n, err := strconv.ParseInt(val, 10, 64)
		if err != nil || n < 5 {
			return fmt.Errorf("报名时间需要是 >=5 的整数秒")
		}
		cfg.SignupTimeout = n
	case "answer_timeout":
		n, err := strconv.Atoi(val)
		if err != nil || n < 5 {
			return fmt.Errorf("答题时间需要是 >=5 的整数秒")
		}
		cfg.AnswerTimeout = n
	case "min_players":
		n, err := strconv.Atoi(val)
		if err != nil || n < 1 {
			return fmt.Errorf("最少人数需要是 >=1 的整数")
		}
		cfg.MinPlayers = n
	case "max_players":
		n, err := strconv.Atoi(val)
		if err != nil || n < cfg.MinPlayers {
			return fmt.Errorf("最多人数需要是 >=最少人数(%d) 的整数", cfg.MinPlayers)
		}
		cfg.MaxPlayers = n
	case "options_count":
		n, err := strconv.Atoi(val)
		if err != nil || n < 2 || n > 9 {
			return fmt.Errorf("选项数需要在 2~9 之间")
		}
		cfg.OptionsCount = n
	case "answer_style":
		if val != "letter" && val != "number" {
			return fmt.Errorf("选项标注方式只能是 letter 或 number")
		}
		cfg.AnswerStyle = val
	case "allow_tie":
		b, err := strconv.ParseBool(val)
		if err != nil {
			return fmt.Errorf("允许平局需要是 true/false")
		}
		cfg.AllowTie = b
	case "eliminate_on_miss":
		b, err := strconv.ParseBool(val)
		if err != nil {
			return fmt.Errorf("超时淘汰需要是 true/false")
		}
		cfg.EliminateOnMiss = b
	case "upload_export":
		b, err := strconv.ParseBool(val)
		if err != nil {
			return fmt.Errorf("上传导出需要是 true/false")
		}
		cfg.UploadExport = b
	case "difficulty_rounds":
		rounds, err := parseDiffRounds(val)
		if err != nil {
			return err
		}
		cfg.DifficultyRounds = rounds
	case "difficulty_names":
		names, err := parseDiffNames(val)
		if err != nil {
			return err
		}
		cfg.DifficultyNames = names
	case "question_types":
		ts := parseTypeList(val)
		if len(ts) == 0 {
			return fmt.Errorf("出题类型至少需要一项, 可选 title/artist/source/album")
		}
		cfg.QuestionTypes = ts
	case "command_prefix", "prefix":
		return fmt.Errorf("指令前缀不支持运行时修改, 请调整 config.json 后重启")
	default:
		return fmt.Errorf("未知配置键: %s (发送「%s配置」查看全部可配置项)", key, prefix)
	}
	return nil
}

// parseDiffRounds 解析 "A=1,B=2,C=3".
func parseDiffRounds(s string) (map[string]int, error) {
	parts := strings.Split(s, ",")
	rounds := make(map[string]int, len(parts))
	for _, p := range parts {
		kv := strings.SplitN(p, "=", 2)
		if len(kv) != 2 {
			return nil, fmt.Errorf("难度曲目格式应为 A=1,B=2,C=3")
		}
		n, err := strconv.Atoi(strings.TrimSpace(kv[1]))
		if err != nil || n < 0 {
			return nil, fmt.Errorf("曲目数需要是 >=0 的整数")
		}
		d := strings.ToUpper(strings.TrimSpace(kv[0]))
		if d == "" {
			return nil, fmt.Errorf("难度标记不能为空")
		}
		rounds[d] = n
	}
	if len(rounds) == 0 {
		return nil, fmt.Errorf("至少需要一个难度")
	}
	return rounds, nil
}

// parseDiffNames 解析 "A=简单,B=普通,C=困难".
func parseDiffNames(s string) (map[string]string, error) {
	parts := strings.Split(s, ",")
	names := make(map[string]string, len(parts))
	for _, p := range parts {
		kv := strings.SplitN(p, "=", 2)
		if len(kv) != 2 {
			return nil, fmt.Errorf("难度名称格式应为 A=简单,B=普通,C=困难")
		}
		names[strings.ToUpper(strings.TrimSpace(kv[0]))] = strings.TrimSpace(kv[1])
	}
	return names, nil
}

// parseTypeList 解析出题类型列表.
func parseTypeList(s string) []string {
	valid := map[string]bool{"title": true, "artist": true, "source": true, "album": true}
	seen := make(map[string]bool)
	out := make([]string, 0, 4)
	for _, sub := range strings.FieldsFunc(strings.ToLower(s),
		func(r rune) bool { return r == ',' || r == '/' || r == ' ' }) {
		if valid[sub] && !seen[sub] {
			seen[sub] = true
			out = append(out, sub)
		}
	}
	return out
}

// showHistory 展示最近对局记录.
func showHistory(ctx *zero.Ctx) {
	hs, err := recentHistory(5)
	if err != nil || len(hs) == 0 {
		ctx.SendChain(message.Text("暂无对局记录~"))
		return
	}
	var sb strings.Builder
	sb.WriteString("最近对局:\n")
	for _, h := range hs {
		sb.WriteString(fmt.Sprintf("#%d %s | 参与:%s | %s\n",
			h.ID, time.Unix(h.Started, 0).Format("01-02 15:04"), h.Players, h.Result))
	}
	ctx.SendChain(message.Text(sb.String()))
}

// NormText 文本归一化: 转小写、全角→半角、去除空白, 用于答案匹配与题目键.
func NormText(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(s)) {
		switch {
		case unicode.IsSpace(r):
			continue
		case r >= '０' && r <= '９':
			b.WriteRune(r - '０' + '0')
		case r >= 'ａ' && r <= 'ｚ':
			b.WriteRune(r - 'ａ' + 'a')
		case r >= 'Ａ' && r <= 'Ｚ':
			b.WriteRune(r - 'Ａ' + 'a')
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// optionLabels 根据配置返回选项标注(如 A,B,C,D 或 1,2,3,4).
func optionLabels(n int) []string {
	labels := make([]string, n)
	if cfg.AnswerStyle == "number" {
		for i := 0; i < n; i++ {
			labels[i] = strconv.Itoa(i + 1)
		}
		return labels
	}
	for i := 0; i < n && i < 26; i++ {
		labels[i] = string(rune('A' + i))
	}
	return labels
}
