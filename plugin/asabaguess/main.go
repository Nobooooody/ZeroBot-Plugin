// Package asabaguess 浅羽猜歌(多人淘汰制多选猜歌)插件
package asabaguess

import (
	"encoding/json"
	"os"

	"github.com/sirupsen/logrus"

	"github.com/FloatTech/floatbox/file"
	ctrl "github.com/FloatTech/zbpctrl"
	"github.com/FloatTech/zbputils/control"
	zero "github.com/wdvxdr1123/ZeroBot"
)

const serviceErr = "[asabaguess]error:"

var (
	prefix   = "浅羽" // 指令前缀, 从配置读取
	cfg      Config
	cfgPath  string
	dbPath   string
	dataDir  = "data/asabaguess/"
	songsDir string // 音频存储目录(模板, %s为难度)
)

func init() {
	engine := control.AutoRegister(&ctrl.Options[*zero.Ctx]{
		DisableOnDefault: false,
		Brief:            "浅羽猜歌",
		Help: "多人淘汰制猜歌. 玩法: 报名后同台竞技, 每轮播放一首歌并给出多选题目,\n" +
			"答错的玩家被淘汰, 通过各难度规定数量的题目后, 尚未被淘汰的玩家获胜(可平局).\n\n" +
			"----------公 用 指 令----------\n" +
			"- 浅羽猜歌      开启报名\n" +
			"- 浅羽报名      报名加入本局\n" +
			"- 浅羽取消猜歌  发起人或管理员取消本局\n" +
			"- 浅羽战绩      查看最近对局记录\n" +
			"- 浅羽题库      查看题库概况\n" +
			"- 浅羽配置      查看当前玩法配置\n\n" +
			"----------管 理 员 指 令---------\n" +
			"- 浅羽设置 <键> <值>  修改玩法配置\n" +
			"   可配置键: signup_timeout / answer_timeout / min_players / max_players /\n" +
			"   options_count / answer_style / allow_tie / eliminate_on_miss / upload_export /\n" +
			"   difficulty_rounds(A=1,B=2,C=3) / difficulty_names(A=简单,B=普通,C=困难) /\n" +
			"   question_types(title,artist,source,album)\n\n" +
			"----------主 人 指 令----------\n" +
			"- 浅羽导入歌包 <文件名|本地路径>  导入资源包(合并/覆盖/取消)\n" +
			"- 浅羽导出歌包  将当前题库与音频打包为资源包上传\n\n" +
			"资源包为 zip, 内含 manifest.json(题目元数据) 与 audios/ 音频目录.\n" +
			"题目字段: difficulty难度 / title歌名 / artist歌手 / source出处 / album专辑.\n",
		PrivateDataFolder: "asabaguess",
	})
	prefix = cfg.CommandPrefix
	if prefix == "" {
		prefix = "浅羽"
		cfg.CommandPrefix = prefix
	}
	dataDir = engine.DataFolder()
	cfgPath = file.BOTPATH + "/" + dataDir + "config.json"
	dbPath = file.BOTPATH + "/" + dataDir + "asabaguess.db"
	songsDir = songsDirBase() + "%s/"
	if err := os.MkdirAll(file.BOTPATH+"/"+dataDir, 0755); err != nil {
		panic(serviceErr + err.Error())
	}
	if err := os.MkdirAll(songsDirBase(), 0755); err != nil {
		panic(serviceErr + err.Error())
	}
	loadConfig()
	if err := openDB(dbPath); err != nil {
		panic(serviceErr + "初始化数据库失败: " + err.Error())
	}
	registerCommands(engine)
}

// songsDirBase 音频根目录(绝对).
func songsDirBase() string {
	return file.BOTPATH + "/" + dataDir + "songs/"
}

// audioAbsPath 题目音频绝对路径.
func audioAbsPath(q *Question) string {
	return songsDirBase() + q.Difficulty + "/" + q.Audio
}

// loadConfig 加载配置, 不存在则写入默认配置.
func loadConfig() {
	if file.IsExist(cfgPath) {
		data, err := os.ReadFile(cfgPath)
		if err == nil {
			err = json.Unmarshal(data, &cfg)
			if err != nil {
				logrus.Warnf("[asabaguess] 解析配置失败, 使用默认配置: %v", err)
				cfg = defaultConfig()
			}
		}
	} else {
		cfg = defaultConfig()
	}
	ensureConfigValid()
	saveConfig()
}

// ensureConfigValid 兜底保证配置合法.
func ensureConfigValid() {
	if cfg.CommandPrefix == "" {
		cfg.CommandPrefix = "浅羽"
	}
	if cfg.SignupTimeout < 5 {
		cfg.SignupTimeout = 60
	}
	if cfg.AnswerTimeout < 5 {
		cfg.AnswerTimeout = 30
	}
	if cfg.MinPlayers < 1 {
		cfg.MinPlayers = 2
	}
	if cfg.MaxPlayers < cfg.MinPlayers {
		cfg.MaxPlayers = cfg.MinPlayers
	}
	if cfg.OptionsCount < 2 || cfg.OptionsCount > 9 {
		cfg.OptionsCount = 4
	}
	if cfg.AnswerStyle != "letter" && cfg.AnswerStyle != "number" {
		cfg.AnswerStyle = "letter"
	}
	if len(cfg.DifficultyRounds) == 0 {
		cfg.DifficultyRounds = map[string]int{"A": 1, "B": 2, "C": 3}
	}
	if len(cfg.DifficultyNames) == 0 {
		cfg.DifficultyNames = map[string]string{"A": "简单", "B": "普通", "C": "困难"}
	}
	for d := range cfg.DifficultyRounds {
		if cfg.DifficultyNames[d] == "" {
			cfg.DifficultyNames[d] = d
		}
	}
	if len(cfg.QuestionTypes) == 0 {
		cfg.QuestionTypes = []string{"title", "artist", "source", "album"}
	}
}

// defaultConfig 默认玩法配置(浅羽原版: A难度1首, B难度2首, C难度3首).
func defaultConfig() Config {
	return Config{
		CommandPrefix: "浅羽",
		SignupTimeout: 60,
		AnswerTimeout: 30,
		MinPlayers:    2,
		MaxPlayers:    8,
		OptionsCount:  4,
		AnswerStyle:   "letter",
		DifficultyRounds: map[string]int{
			"A": 1,
			"B": 2,
			"C": 3,
		},
		DifficultyNames: map[string]string{
			"A": "简单",
			"B": "普通",
			"C": "困难",
		},
		QuestionTypes:   []string{"title", "artist", "source", "album"},
		AllowTie:        true,
		EliminateOnMiss: true,
		UploadExport:    true,
	}
}

// saveConfig 保存配置.
func saveConfig() {
	prefix = cfg.CommandPrefix
	if prefix == "" {
		prefix = "浅羽"
	}
	data, err := json.MarshalIndent(&cfg, "", "  ")
	if err != nil {
		logrus.Warnf("[asabaguess] 序列化配置失败: %v", err)
		return
	}
	if err := os.WriteFile(cfgPath, data, 0644); err != nil {
		logrus.Warnf("[asabaguess] 写入配置失败: %v", err)
	}
}
