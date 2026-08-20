// Package asabaguess 浅羽猜歌(多人淘汰制多选猜歌)插件
package asabaguess

import (
	"fmt"
	"sync"
	"time"

	"github.com/sirupsen/logrus"

	sql "github.com/FloatTech/sqlite"
)

// Config 浅羽猜歌的可配置项.
// 所有玩法参数均通过 config.json 及"浅羽设置"指令动态调整, 不写入代码.
type Config struct {
	CommandPrefix string `json:"command_prefix"` // 指令前缀, 默认"浅羽"
	SignupTimeout int64  `json:"signup_timeout"` // 报名窗口时长(秒)
	AnswerTimeout int    `json:"answer_timeout"` // 单题作答时限(秒)
	MinPlayers    int    `json:"min_players"`    // 最少参与人数
	MaxPlayers    int    `json:"max_players"`    // 最多参与人数
	OptionsCount  int    `json:"options_count"`  // 每道题的选项数(2~9)
	AnswerStyle   string `json:"answer_style"`   // 选项标注方式 letter(A~Z) 或 number(1~n)
	// DifficultyRounds 各难度需通过的曲目数, 如 {"A":1,"B":2,"C":3}
	DifficultyRounds map[string]int `json:"difficulty_rounds"`
	// DifficultyNames 各难度显示名, 如 {"A":"简单","B":"普通","C":"困难"}
	DifficultyNames map[string]string `json:"difficulty_names"`
	// QuestionTypes 可出题类型: title(歌名) artist(歌手) source(出处) album(专辑)
	QuestionTypes   []string `json:"question_types"`
	AllowTie        bool     `json:"allow_tie"`         // 结束时存活多人是否允许平局
	EliminateOnMiss bool     `json:"eliminate_on_miss"` // 超时未作答是否淘汰
	UploadExport    bool     `json:"upload_export"`     // 导出歌包后是否上传到群文件
}

// Question 题库条目.
type Question struct {
	ID         int64  `db:"id"`         // 自增主键
	Difficulty string `db:"difficulty"` // 难度标记 A/B/C...
	Title      string `db:"title"`      // 歌名
	Artist     string `db:"artist"`     // 歌手
	Source     string `db:"source"`     // 出处(动漫等)
	Album      string `db:"album"`      // 专辑/作曲
	Audio      string `db:"audio"`      // 音频文件名(相对songsDir/<difficulty>/)
	AudioMD5   string `db:"audio_md5"`  // 音频md5, 用于去重
	Enabled    bool   `db:"enabled"`    // 是否启用
}

// History 对局历史.
type History struct {
	ID       int64  `db:"id"`
	GroupID  int64  `db:"group_id"`
	Players  string `db:"players"` // 参与玩家, 逗号分隔
	Winner   string `db:"winner"`  // 胜者, 逗号分隔
	Result   string `db:"result"`  // 结果描述
	Rounds   int    `db:"rounds"`  // 进行轮数
	Started  int64  `db:"started"` // 开始时间戳
	Finished int64  `db:"finished"`
}

// maxID 用于查询 COALESCE(MAX(id),0).
type maxID struct {
	ID int64
}

var (
	// db 题库与战绩数据库.
	db sql.Sqlite
	// dbmu 数据库写锁.
	dbmu sync.Mutex
	// nextQID 下一个题目自增id(该库无AUTOINCREMENT, 需自行维护).
	nextQID int64
)

// difficultyName 返回难度显示名, 未配置则回退原标记.
func (c *Config) difficultyName(diff string) string {
	if name, ok := c.DifficultyNames[diff]; ok && name != "" {
		return name
	}
	return diff
}

// sortedDifficulties 按字符序返回已配置的难度序列.
func (c *Config) sortedDifficulties() []string {
	keys := make([]string, 0, len(c.DifficultyRounds))
	for d := range c.DifficultyRounds {
		keys = append(keys, d)
	}
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j] < keys[j-1]; j-- {
			keys[j], keys[j-1] = keys[j-1], keys[j]
		}
	}
	return keys
}

// openDB 打开数据库并初始化表结构.
func openDB(dbpath string) error {
	dbmu.Lock()
	defer dbmu.Unlock()
	db = sql.New(dbpath)
	if err := db.Open(time.Hour); err != nil {
		return err
	}
	if err := db.Create("question", &Question{}); err != nil {
		return err
	}
	if err := db.Create("history", &History{}); err != nil {
		return err
	}
	var mx maxID
	err := db.Query("SELECT COALESCE(MAX(id),0) FROM question", &mx)
	if err != nil {
		logrus.Warnf("[asabaguess] 读取题目最大id失败: %v", err)
	}
	nextQID = mx.ID + 1
	return nil
}

// ---------------- 题库查询 ----------------

// loadQuestions 载入全部启用题目.
func loadQuestions() (qs []*Question, err error) {
	qs, err = sql.FindAll[Question](&db, "question", "WHERE enabled = 1")
	return
}

// loadQuestionsByDifficulty 载入某难度下启用的题目.
func loadQuestionsByDifficulty(diff string) (qs []*Question, err error) {
	qs, err = sql.FindAll[Question](&db, "question", "WHERE difficulty = ? AND enabled = 1", diff)
	return
}

// insertQuestion 插入新题目, 自动分配 id, 已存在则覆盖.
func insertQuestion(q *Question) error {
	dbmu.Lock()
	defer dbmu.Unlock()
	if q.ID == 0 {
		q.ID = nextQID
		nextQID++
	}
	return db.Insert("question", q)
}

// deleteQuestion 删除题目.
func deleteQuestion(id int64) error {
	dbmu.Lock()
	defer dbmu.Unlock()
	return db.Del("question", "WHERE id = ?", id)
}

// findQuestionByKey 按 难度/歌名/歌手 查找启用中的题目(用于导入时判断新旧).
func findQuestionByKey(diff, title, artist string) (q *Question, ok bool) {
	qs, err := loadQuestionsByDifficulty(diff)
	if err != nil {
		return
	}
	key := questionKey(diff, title, artist)
	for _, item := range qs {
		if questionKey(item.Difficulty, item.Title, item.Artist) == key {
			q = item
			ok = true
			return
		}
	}
	return
}

// questionKey 题目唯一键: 难度+歌名+歌手(归一化).
func questionKey(diff, title, artist string) string {
	return diff + "|" + NormText(title) + "|" + NormText(artist)
}

// questionCaption 生成题目的一行摘要(导入时展示).
func (q *Question) caption() string {
	return fmt.Sprintf("[%s] 《%s》 - %s", q.Difficulty, q.Title, q.Artist)
}

// ---------------- 对局历史 ----------------

// appendHistory 保存对局历史.
func appendHistory(h *History) error {
	dbmu.Lock()
	defer dbmu.Unlock()
	if h.ID == 0 {
		var mx maxID
		err := db.Query("SELECT COALESCE(MAX(id),0) FROM history", &mx)
		if err == nil {
			h.ID = mx.ID + 1
		}
	}
	return db.Insert("history", h)
}

// recentHistory 返回最近若干条战绩.
func recentHistory(limit int) (hs []*History, err error) {
	hs, err = sql.FindAll[History](&db, "history",
		fmt.Sprintf("ORDER BY id DESC LIMIT %d", limit))
	return
}
