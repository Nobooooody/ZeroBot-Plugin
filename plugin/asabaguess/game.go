package asabaguess

import (
	"fmt"
	"math/rand"
	"strconv"
	"strings"
	"sync"
	"time"

	zero "github.com/wdvxdr1123/ZeroBot"
	"github.com/wdvxdr1123/ZeroBot/message"
)

// gamePhase 房间阶段.
type gamePhase int

const (
	phaseSignup  gamePhase = iota // 报名阶段
	phasePlaying                  // 对局阶段
)

type player struct {
	uid   int64
	name  string
	alive bool
}

// game 一场对局的全部状态.
type game struct {
	gid       int64
	host      int64
	hostName  string
	phase     gamePhase
	signupEnd time.Time
	players   []*player
	done      chan struct{}
	doneOnce  sync.Once
}

// alive 房间是否仍存活(存在且未取消).
func (g *game) alive() bool {
	select {
	case <-g.done:
		return false
	default:
		return true
	}
}

// playerByUID 查找玩家.
func (g *game) playerByUID(uid int64) *player {
	for _, p := range g.players {
		if p.uid == uid {
			return p
		}
	}
	return nil
}

// alivePlayers 存活玩家列表.
func (g *game) alivePlayers() (ps []*player) {
	for _, p := range g.players {
		if p.alive {
			ps = append(ps, p)
		}
	}
	return
}

// liveNames 存活玩家名.
func (g *game) liveNames() (names []string) {
	for _, p := range g.alivePlayers() {
		names = append(names, p.name)
	}
	return
}

var (
	gameMu    sync.Mutex
	gameRooms = make(map[int64]*game)
)

// getRoom 获取某群房间.
func getRoom(gid int64) *game {
	gameMu.Lock()
	defer gameMu.Unlock()
	return gameRooms[gid]
}

// cancelRoom 取消并删除房间.
func cancelRoom(gid int64) {
	gameMu.Lock()
	r := gameRooms[gid]
	delete(gameRooms, gid)
	gameMu.Unlock()
	if r != nil {
		r.doneOnce.Do(func() { close(r.done) })
	}
}

// beginSignup 「浅羽猜歌」: 开启报名.
func beginSignup(ctx *zero.Ctx) {
	gid := ctx.Event.GroupID
	hostName := ctx.CardOrNickName(ctx.Event.UserID)
	gameMu.Lock()
	if _, ok := gameRooms[gid]; ok {
		gameMu.Unlock()
		ctx.SendChain(message.Text("本群已有进行中的猜歌, 请等它结束再开新局哦~"))
		return
	}
	r := &game{
		gid:       gid,
		host:      ctx.Event.UserID,
		hostName:  hostName,
		phase:     phaseSignup,
		signupEnd: time.Now().Add(time.Duration(cfg.SignupTimeout) * time.Second),
		done:      make(chan struct{}),
	}
	gameRooms[gid] = r
	gameMu.Unlock()
	ctx.SendChain(message.Text(fmt.Sprintf(
		"🎵 猜歌报名开始啦!\n本局为多人淘汰制, 答错即被淘汰.\n"+
			"发送「%s报名」加入本局, 报名窗口 %d 秒.\n"+
			"难度安排: %s.\n发起人 %s 可发送「%s取消猜歌」结束本局.",
		prefix, cfg.SignupTimeout, roundsText(), hostName, prefix)))
	go r.run(ctx)
}

// roundsText 难度安排文案.
func roundsText() string {
	var parts []string
	for _, d := range cfg.sortedDifficulties() {
		parts = append(parts, fmt.Sprintf("%s(%s)%d首", d, cfg.difficultyName(d), cfg.DifficultyRounds[d]))
	}
	return strings.Join(parts, " → ")
}

// joinSignup 「浅羽报名」: 加入报名.
func joinSignup(ctx *zero.Ctx) {
	gid := ctx.Event.GroupID
	uid := ctx.Event.UserID
	gameMu.Lock()
	r := gameRooms[gid]
	if r == nil || r.phase != phaseSignup {
		gameMu.Unlock()
		ctx.SendChain(message.Text("当前没有报名中的猜歌, 发送「", prefix, "猜歌」开启一局吧~"))
		return
	}
	if r.playerByUID(uid) != nil {
		gameMu.Unlock()
		ctx.SendChain(message.Text("你已经报名参与本局啦~"))
		return
	}
	if len(r.players) >= cfg.MaxPlayers {
		gameMu.Unlock()
		ctx.SendChain(message.Text(fmt.Sprintf("报名人数已满(%d人), 等开赛吧~", cfg.MaxPlayers)))
		return
	}
	name := ctx.CardOrNickName(uid)
	r.players = append(r.players, &player{uid: uid, name: name, alive: true})
	n := len(r.players)
	gameMu.Unlock()
	ctx.SendChain(message.Text(fmt.Sprintf("%s 加入本局! 当前共 %d 人.", name, n)))
}

// cancelGame 「浅羽取消猜歌」: 取消本局.
func cancelGame(ctx *zero.Ctx) {
	gid := ctx.Event.GroupID
	r := getRoom(gid)
	if r == nil {
		ctx.SendChain(message.Text("当前没有进行中的猜歌~"))
		return
	}
	if ctx.Event.UserID != r.host && !zero.AdminPermission(ctx) {
		ctx.SendChain(message.Text("只有发起人或管理员才能取消本局猜歌~"))
		return
	}
	cancelRoom(gid)
	ctx.SendChain(message.Text("本局猜歌已取消~"))
}

// run 对局主流程: 等待报名结束后开始逐难度逐轮.
func (g *game) run(ctx *zero.Ctx) {
	defer cancelRoom(g.gid)
	// -------- 报名等待 & 倒计时提醒 --------
	target := g.signupEnd
	half := time.Duration(cfg.SignupTimeout/2) * time.Second
	reminded := false
	for {
		select {
		case <-g.done:
			return
		default:
		}
		now := time.Now()
		if !reminded && now.After(target.Add(-half)) && now.Before(target) {
			reminded = true
			ctx.SendChain(message.Text(fmt.Sprintf("⏰ 报名将在 %d 秒后截止, 想玩的快发送「%s报名」~",
				int(target.Sub(now).Seconds()), prefix)))
		}
		if now.After(target) {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	if !g.alive() {
		return
	}
	// -------- 开赛条件 --------
	if len(g.players) < cfg.MinPlayers {
		ctx.SendChain(message.Text(fmt.Sprintf("报名人数不足(需要≥%d人), 本局取消~", cfg.MinPlayers)))
		return
	}
	gameMu.Lock()
	g.phase = phasePlaying
	gameMu.Unlock()
	qs, err := loadQuestions()
	if err != nil || len(qs) == 0 {
		ctx.SendChain(message.Text("题库为空, 无法开赛, 请主人先导入资源包~"))
		return
	}
	playerNames := make([]string, 0, len(g.players))
	for i, p := range g.players {
		playerNames = append(playerNames, fmt.Sprintf("%s %s", optionLabel(i), p.name))
	}
	ctx.SendChain(message.Text(fmt.Sprintf("🎵 猜歌开赛!\n参赛选手:\n%s\n\n%s", strings.Join(playerNames, "\n"), roundsText())))

	startT := time.Now().Unix()
	roundsPlayed := 0
	diffs := cfg.sortedDifficulties()
	total := len(diffs)
	for di, diff := range diffs {
		required := cfg.DifficultyRounds[diff]
		if required <= 0 {
			continue
		}
		pool := make([]*Question, 0, len(qs))
		for _, q := range qs {
			if q.Difficulty == diff {
				pool = append(pool, q)
			}
		}
		if len(pool) == 0 {
			ctx.SendChain(message.Text(fmt.Sprintf("难度 %s(%s) 暂无题目, 已跳过~", diff, cfg.difficultyName(diff))))
			continue
		}
		used := make(map[int64]bool)
		for ri := 1; ri <= required; ri++ {
			res := g.playRound(ctx, diff, di+1, total, ri, required, pool, qs, used)
			roundsPlayed++
			if res == roundCancelled {
				return
			}
			if res == roundAllEliminated {
				g.finish(ctx, startT, roundsPlayed)
				return
			}
		}
	}
	g.finish(ctx, startT, roundsPlayed)
}

type roundResult int

const (
	roundContinue      roundResult = iota
	roundAllEliminated             // 全员淘汰
	roundCancelled                 // 房间被取消
)

// playRound 进行一轮: 出题、收集答案、判定.
func (g *game) playRound(ctx *zero.Ctx, diff string, di, total, ri, required int, pool, all []*Question, used map[int64]bool) roundResult {
	q, qtype := pickQuestion(pool, all, used)
	if q == nil {
		ctx.SendChain(message.Text("当前难度题库无法出题, 已跳过~"))
		return roundContinue
	}
	used[q.ID] = true
	diffName := cfg.difficultyName(diff)

	texts, correctIdx := buildOptions(q, qtype, all, cfg.OptionsCount)
	labels := optionLabels(len(texts))
	if correctIdx < 0 || len(texts) < 2 {
		return roundContinue
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("第 %d/%d 题 · 难度 %s(%d/%d)\n", ri, required, diffName, di, total))
	sb.WriteString(fmt.Sprintf("🎤 题目(%s): 这首歌的%s是?\n", typeDisplayName(qtype), typeFieldName(qtype)))
	for i, t := range texts {
		sb.WriteString(fmt.Sprintf("%s. %s\n", labels[i], t))
	}
	sb.WriteString(fmt.Sprintf("请在 %d 秒内作答, 发送选项或内容即可~", cfg.AnswerTimeout))
	ctx.SendChain(
		message.Record("file:///"+audioAbsPath(q)),
		message.Text(sb.String()),
	)

	// -------- 收集答案 --------
	next := zero.NewFutureEvent("message", 999, false, zero.OnlyGroup, zero.CheckGroup(g.gid))
	recv, cancel := next.Repeat()
	defer cancel()
	timer := time.NewTimer(time.Duration(cfg.AnswerTimeout) * time.Second)
	answered := make(map[int64]bool)
	var winners, losers []string
	for {
		select {
		case <-g.done:
			ctx.SendChain(message.Text("本局猜歌已取消~"))
			return roundCancelled
		case <-timer.C:
			// 超时未答者, 按配置处理
			gameMu.Lock()
			for _, p := range g.players {
				if p.alive && !answered[p.uid] {
					if cfg.EliminateOnMiss {
						p.alive = false
						losers = append(losers, p.name+"(超时)")
					} else {
						winners = append(winners, p.name)
					}
				}
			}
			gameMu.Unlock()
			goto reveal
		case c := <-recv:
			uid := c.Event.UserID
			msgStr := c.Event.RawMessage
			gameMu.Lock()
			p := g.playerByUID(uid)
			if p == nil || !p.alive || answered[uid] {
				gameMu.Unlock()
				continue
			}
			idx, matched := matchAnswer(msgStr, labels, texts)
			if !matched {
				gameMu.Unlock()
				ctx.Send(message.ReplyWithMessage(c.Event.MessageID,
					message.Text("回答格式有误, 请发送选项标注(如 A/B/C)或选项内容~")))
				continue
			}
			answered[uid] = true
			if idx == correctIdx {
				winners = append(winners, p.name)
				gameMu.Unlock()
				ctx.Send(message.ReplyWithMessage(c.Event.MessageID, message.Text("✅ 回答正确!")))
			} else {
				p.alive = false
				losers = append(losers, p.name)
				gameMu.Unlock()
				ctx.Send(message.ReplyWithMessage(c.Event.MessageID, message.Text("❌ 回答错误, 被淘汰!")))
			}
		}
	}

reveal:
	timer.Stop()
	ctx.SendChain(message.Text(fmt.Sprintf("🔔 本道题正确答案: %s. %s", labels[correctIdx], texts[correctIdx])))
	if len(losers) > 0 {
		ctx.SendChain(message.Text("💀 本轮被淘汰: " + strings.Join(losers, "、")))
	}
	alives := g.alivePlayers()
	if len(alives) == 0 {
		ctx.SendChain(message.Text("全员淘汰, 本局结束, 无人获胜~"))
		return roundAllEliminated
	}
	if len(winners) > 0 {
		ctx.SendChain(message.Text(fmt.Sprintf("👏 本轮通过: %s\n剩余存活 %d 人: %s",
			strings.Join(winners, "、"), len(alives), strings.Join(g.liveNames(), "、"))))
	} else {
		ctx.SendChain(message.Text(fmt.Sprintf("本轮无人答对~ 剩余存活 %d 人: %s",
			len(alives), strings.Join(g.liveNames(), "、"))))
	}
	return roundContinue
}

// finish 对局收尾.
func (g *game) finish(ctx *zero.Ctx, started int64, roundsPlayed int) {
	survivors := g.liveNames()
	result := ""
	winner := ""
	switch len(survivors) {
	case 0:
		result = "全员淘汰, 无人获胜"
	case 1:
		winner = survivors[0]
		result = "🏆 获胜者: " + survivors[0]
	default:
		winner = strings.Join(survivors, "、")
		if cfg.AllowTie {
			result = "🤝 平局, 共同胜出: " + winner
		} else {
			result = "本局结束, 仍存活: " + winner
		}
	}
	ctx.SendChain(message.Text(fmt.Sprintf("🎉 猜歌结束!\n%s\n共进行 %d 轮.", result, roundsPlayed)))
	names := make([]string, 0, len(g.players))
	for _, p := range g.players {
		names = append(names, p.name)
	}
	_ = appendHistory(&History{
		GroupID:  g.gid,
		Players:  strings.Join(names, "、"),
		Winner:   winner,
		Result:   result,
		Rounds:   roundsPlayed,
		Started:  started,
		Finished: time.Now().Unix(),
	})
}

// pickQuestion 从当前难度选题(避开已出过的), 同时决定出题类型.
func pickQuestion(pool, all []*Question, used map[int64]bool) (*Question, string) {
	var fresh []*Question
	for _, q := range pool {
		if !used[q.ID] {
			fresh = append(fresh, q)
		}
	}
	if len(fresh) == 0 {
		fresh = pool
	}
	for attempt := 0; attempt < 10; attempt++ {
		q := fresh[rand.Intn(len(fresh))]
		qtype := randomTypeWithOptions(q, all)
		if qtype != "" {
			return q, qtype
		}
	}
	return nil, ""
}

// randomTypeWithOptions 随机挑选一个能凑出题目的类型.
func randomTypeWithOptions(q *Question, all []*Question) string {
	types := make([]string, len(cfg.QuestionTypes))
	copy(types, cfg.QuestionTypes)
	rand.Shuffle(len(types), func(i, j int) { types[i], types[j] = types[j], types[i] })
	for _, t := range types {
		if fieldValue(q, t) == "" {
			continue
		}
		texts, idx := buildOptions(q, t, all, 2)
		if idx >= 0 && len(texts) >= 2 {
			return t
		}
	}
	return ""
}

// buildOptions 构造题目多选选项, 返回选项文案与正确项下标.
func buildOptions(q *Question, qtype string, all []*Question, need int) (texts []string, correctIdx int) {
	correct := strings.TrimSpace(fieldValue(q, qtype))
	if correct == "" {
		return nil, -1
	}
	correctIdx = -1
	seen := map[string]bool{NormText(correct): true}
	var cands []string
	for _, o := range all {
		if o.ID == q.ID {
			continue
		}
		v := strings.TrimSpace(fieldValue(o, qtype))
		if v == "" {
			continue
		}
		if seen[NormText(v)] {
			continue
		}
		seen[NormText(v)] = true
		cands = append(cands, v)
	}
	rand.Shuffle(len(cands), func(i, j int) { cands[i], cands[j] = cands[j], cands[i] })
	if need > len(cands)+1 {
		need = len(cands) + 1
	}
	if need < 2 {
		return nil, -1
	}
	texts = make([]string, 0, need)
	texts = append(texts, correct)
	for i := 0; i < need-1 && i < len(cands); i++ {
		texts = append(texts, cands[i])
	}
	correctIdx = rand.Intn(len(texts))
	texts[0], texts[correctIdx] = texts[correctIdx], texts[0]
	return texts, correctIdx
}

// fieldValue 取题目对应字段值.
func fieldValue(q *Question, qtype string) string {
	switch qtype {
	case "title":
		return q.Title
	case "artist":
		return q.Artist
	case "source":
		return q.Source
	case "album":
		return q.Album
	}
	return ""
}

// typeFieldName 题型对应中文.
func typeFieldName(qtype string) string {
	switch qtype {
	case "title":
		return "歌名"
	case "artist":
		return "歌手"
	case "source":
		return "出处(动漫等)"
	case "album":
		return "专辑/作曲"
	}
	return qtype
}

// typeDisplayName 题型展示名.
func typeDisplayName(qtype string) string {
	switch qtype {
	case "title":
		return "歌名"
	case "artist":
		return "歌手"
	case "source":
		return "出处"
	case "album":
		return "专辑"
	}
	return qtype
}

// optionLabel 第 i 个玩家的标注(如 A/B/C/D).
func optionLabel(i int) string {
	if cfg.AnswerStyle == "number" {
		return strconv.Itoa(i + 1)
	}
	return string(rune('A' + i))
}

// matchAnswer 匹配玩家答案, 支持选项标注或选项内容(归一化后模糊匹配).
func matchAnswer(raw string, labels []string, texts []string) (idx int, ok bool) {
	s := NormText(raw)
	if s == "" {
		return 0, false
	}
	for i, l := range labels {
		if s == NormText(l) {
			return i, true
		}
	}
	if n, err := strconv.Atoi(s); err == nil && n >= 1 && n <= len(texts) {
		return n - 1, true
	}
	for i, t := range texts {
		nt := NormText(t)
		if nt != "" && len(nt) <= len(s)+2 && (strings.Contains(nt, s) || strings.Contains(s, nt)) {
			return i, true
		}
	}
	return 0, false
}
