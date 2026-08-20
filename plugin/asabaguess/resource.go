package asabaguess

import (
	"archive/zip"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/FloatTech/floatbox/file"
	zero "github.com/wdvxdr1123/ZeroBot"
	"github.com/wdvxdr1123/ZeroBot/message"
)

// packManifest 资源包清单(manifest.json).
type packManifest struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Format      int    `json:"format"`
	ExportedAt  string `json:"exported_at,omitempty"`
	// Items 题目条目集.
	Items []packItem `json:"items"`
}

// packItem 资源包内的一道题.
type packItem struct {
	ID         int64  `json:"id,omitempty"` // 导出时的源题号, 可选
	Difficulty string `json:"difficulty"`   // A/B/C...
	Title      string `json:"title"`        // 歌名
	Artist     string `json:"artist"`       // 歌手
	Source     string `json:"source"`       // 出处
	Album      string `json:"album"`        // 专辑/作曲
	Audio      string `json:"audio"`        // zip 内相对路径, 如 audios/A/xxx.mp3
	AudioMD5   string `json:"audio_md5,omitempty"`
}

const manifestFile = "manifest.json"

// exportPack 「浅羽导出歌包」: 将当前题库与音频打成资源包.
func exportPack(ctx *zero.Ctx) {
	qs, err := loadQuestions()
	if err != nil || len(qs) == 0 {
		ctx.SendChain(message.Text("题库为空, 无可导出的内容~"))
		return
	}
	if r := getRoom(ctx.Event.GroupID); r != nil {
		ctx.SendChain(message.Text("对局进行中, 请结束后再导出~"))
		return
	}
	man := packManifest{
		Name:       "asabaguess资源包",
		Format:     1,
		ExportedAt: time.Now().Format("2006-01-02 15:04:05"),
		Items:      make([]packItem, 0, len(qs)),
	}
	for _, q := range qs {
		abs := audioAbsPath(q)
		if file.IsNotExist(abs) {
			continue // 缺失音频的题目不导出
		}
		man.Items = append(man.Items, packItem{
			ID:         q.ID,
			Difficulty: q.Difficulty,
			Title:      q.Title,
			Artist:     q.Artist,
			Source:     q.Source,
			Album:      q.Album,
			Audio:      "audios/" + q.Difficulty + "/" + q.Audio,
			AudioMD5:   md5FileHex(abs),
		})
	}
	if len(man.Items) == 0 {
		ctx.SendChain(message.Text("题库中没有可导出的题目(音频缺失)~"))
		return
	}
	outDir := file.BOTPATH + "/" + dataDir + "export/"
	if err := os.MkdirAll(outDir, 0755); err != nil {
		ctx.SendChain(message.Text(serviceErr, err))
		return
	}
	name := fmt.Sprintf("asabaguess_%s.zip", time.Now().Format("20060102_150405"))
	zipPath := outDir + name
	if err := writePack(zipPath, &man, qs); err != nil {
		ctx.SendChain(message.Text(serviceErr, err))
		return
	}
	ctx.SendChain(message.Text(fmt.Sprintf("✅ 资源包导出成功!\n共 %d 题. 本地路径: %s", len(man.Items), zipPath)))
	if cfg.UploadExport {
		resp := ctx.UploadThisGroupFile(zipPath, name, "")
		if resp.Status == "ok" {
			ctx.SendChain(message.Text("已上传到群文件, 文件名: " + name))
		} else {
			ctx.SendChain(message.Text("上传群文件失败(", resp.Wording, "), 仍是本地路径可用"))
		}
	}
}

// writePack 将题库与音频写入 zip.
func writePack(zipPath string, man *packManifest, qs []*Question) error {
	f, err := os.Create(zipPath)
	if err != nil {
		return err
	}
	defer f.Close()
	zw := zip.NewWriter(f)
	defer zw.Close()
	// manifest.json
	manBytes, err := json.MarshalIndent(man, "", "  ")
	if err != nil {
		return err
	}
	manW, err := zw.Create(manifestFile)
	if err != nil {
		return err
	}
	if _, err := manW.Write(manBytes); err != nil {
		return err
	}
	// audios...
	for _, q := range qs {
		abs := audioAbsPath(q)
		if file.IsNotExist(abs) {
			continue
		}
		entry := "audios/" + q.Difficulty + "/" + q.Audio
		w, err := zw.Create(entry)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(abs)
		if err != nil {
			return err
		}
		if _, err := w.Write(data); err != nil {
			return err
		}
	}
	return nil
}

// importPack 「浅羽导入歌包 <文件名|本地路径>」: 导入资源包, 支持合并/覆盖/取消.
func importPack(ctx *zero.Ctx) {
	arg := strings.TrimSpace(ctx.State["args"].(string))
	if arg == "" {
		ctx.SendChain(message.Text("用法: ", prefix, "导入歌包 <群文件名|本地路径>"))
		return
	}
	if r := getRoom(ctx.Event.GroupID); r != nil {
		ctx.SendChain(message.Text("对局进行中, 请结束后再导入~"))
		return
	}
	// 1. 获取资源包文件
	zipPath, err := locatePack(ctx, arg)
	if err != nil {
		ctx.SendChain(message.Text(serviceErr, err))
		return
	}
	tmpDir := file.BOTPATH + "/" + dataDir + "tmp/"
	if err := os.MkdirAll(tmpDir, 0755); err != nil {
		ctx.SendChain(message.Text(serviceErr, err))
		return
	}
	localZip := tmpDir + "import_" + time.Now().Format("20060102_150405") + ".zip"
	if zipPath != localZip {
		if err := copyFile(zipPath, localZip); err != nil {
			ctx.SendChain(message.Text(serviceErr, err))
			return
		}
	}
	// 2. 解析清单
	man, err := readManifest(localZip)
	if err != nil {
		ctx.SendChain(message.Text("资源包解析失败: ", err))
		return
	}
	ctx.SendChain(message.Text(fmt.Sprintf(
		"📦 资源包「%s」解析成功, 共 %d 题.\n请选择导入方式, 回复序号:\n1. 合并(逐条确认替换)\n2. 覆盖(清空现有题库与音频后导入)\n3. 取消",
		man.Name, len(man.Items))))
	mode, ok := awaitImportMode(ctx)
	if !ok {
		return
	}
	switch mode {
	case 3:
		ctx.SendChain(message.Text("已取消导入~"))
		return
	case 2:
		if !awaitConfirmOverwrite(ctx) {
			ctx.SendChain(message.Text("已取消覆盖导入~"))
			return
		}
		// 清空现有题库与音频
		if err := wipeQuestionBank(); err != nil {
			ctx.SendChain(message.Text(serviceErr, err))
			return
		}
	case 1: // 合并
	}

	// 3. 执行导入
	newCount, replaced, skipped, err := applyPack(ctx, localZip, man, mode == 2)
	if err != nil {
		ctx.SendChain(message.Text(serviceErr, err))
		return
	}
	ctx.SendChain(message.Text(fmt.Sprintf("✅ 导入完成!\n新增 %d 题, 替换 %d 题, 跳过 %d 题.",
		newCount, replaced, skipped)))
}

// locatePack 定位资源包: 本地绝对路径优先, 否则按群文件名搜索.
func locatePack(ctx *zero.Ctx, arg string) (string, error) {
	if strings.HasPrefix(arg, "/") || strings.Contains(arg, ":\\") || strings.Contains(arg, ":/") {
		if file.IsExist(arg) {
			return arg, nil
		}
		return "", fmt.Errorf("本地路径不存在: %s", arg)
	}
	// 搜索群文件
	name, url := findGroupFileByName(ctx, arg)
	if name == "" || url == "" {
		return "", fmt.Errorf("未找到群文件「%s」, 请确认文件名或改用本地绝对路径", arg)
	}
	dst := file.BOTPATH + "/" + dataDir + "tmp/" + name
	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return "", err
	}
	if err := file.DownloadTo(url, dst); err != nil {
		return "", fmt.Errorf("下载群文件失败: %v", err)
	}
	return dst, nil
}

// findGroupFileByName 递归查找群文件.
func findGroupFileByName(ctx *zero.Ctx, want string) (name, url string) {
	files := ctx.GetThisGroupRootFiles()
	for _, f := range files.Get("files").Array() {
		if strings.Contains(f.Get("file_name").String(), want) {
			return f.Get("file_name").String(),
				ctx.GetThisGroupFileURL(f.Get("busid").Int(), f.Get("file_id").String())
		}
	}
	for _, fd := range files.Get("folders").Array() {
		if n, u := findGroupFileInFolder(ctx, fd.Get("folder_id").String(), want); u != "" {
			return n, u
		}
	}
	return "", ""
}

func findGroupFileInFolder(ctx *zero.Ctx, folderID, want string) (name, url string) {
	files := ctx.GetThisGroupFilesByFolder(folderID)
	for _, f := range files.Get("files").Array() {
		if strings.Contains(f.Get("file_name").String(), want) {
			return f.Get("file_name").String(),
				ctx.GetThisGroupFileURL(f.Get("busid").Int(), f.Get("file_id").String())
		}
	}
	for _, fd := range files.Get("folders").Array() {
		if n, u := findGroupFileInFolder(ctx, fd.Get("folder_id").String(), want); u != "" {
			return n, u
		}
	}
	return "", ""
}

// readManifest 读取 zip 内 manifest.json.
func readManifest(zipPath string) (*packManifest, error) {
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		return nil, err
	}
	defer zr.Close()
	for _, f := range zr.File {
		if f.Name == manifestFile {
			rc, err := f.Open()
			if err != nil {
				return nil, err
			}
			defer rc.Close()
			data, err := io.ReadAll(rc)
			if err != nil {
				return nil, err
			}
			var man packManifest
			if err := json.Unmarshal(data, &man); err != nil {
				return nil, err
			}
			if len(man.Items) == 0 {
				return nil, fmt.Errorf("资源包内没有任何题目")
			}
			return &man, nil
		}
	}
	return nil, fmt.Errorf("资源包缺少 %s", manifestFile)
}

// readZipEntry 读取 zip 内某条目的内容.
func readZipEntry(zipPath, entry string) ([]byte, error) {
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		return nil, err
	}
	defer zr.Close()
	for _, f := range zr.File {
		if f.Name == entry {
			rc, err := f.Open()
			if err != nil {
				return nil, err
			}
			defer rc.Close()
			return io.ReadAll(rc)
		}
	}
	return nil, fmt.Errorf("zip 内缺少条目 %s", entry)
}

// awaitImportMode 等待选择导入方式. 返回 1(合并)/2(覆盖)/3(取消).
func awaitImportMode(ctx *zero.Ctx) (mode int, ok bool) {
	next := zero.NewFutureEvent("message", 999, false, zero.OnlyGroup, ctx.CheckSession())
	recv, cancel := next.Repeat()
	defer cancel()
	timer := time.NewTimer(60 * time.Second)
	for {
		select {
		case <-timer.C:
			ctx.SendChain(message.Text("选择超时, 已取消导入~"))
			return 0, false
		case c := <-recv:
			s := strings.TrimSpace(c.Event.Message.String())
			switch s {
			case "1", "合并":
				return 1, true
			case "2", "覆盖":
				return 2, true
			case "3", "取消":
				return 3, true
			default:
				ctx.SendChain(message.Text("请回复 1(合并)/2(覆盖)/3(取消)~"))
			}
		}
	}
}

// awaitConfirmOverwrite 覆盖前二次确认.
func awaitConfirmOverwrite(ctx *zero.Ctx) bool {
	ctx.SendChain(message.Text("⚠️ 覆盖导入将【清空当前题库与所有音频】, 是否继续? 回复「确认覆盖」继续, 其他任意内容取消~"))
	next := zero.NewFutureEvent("message", 999, false, zero.OnlyGroup, ctx.CheckSession())
	recv, cancel := next.Repeat()
	defer cancel()
	timer := time.NewTimer(60 * time.Second)
	for {
		select {
		case <-timer.C:
			ctx.SendChain(message.Text("确认超时, 已取消覆盖导入~"))
			return false
		case c := <-recv:
			if strings.TrimSpace(c.Event.Message.String()) == "确认覆盖" {
				return true
			}
			return false
		}
	}
}

// wipeQuestionBank 清空题库与音频.
func wipeQuestionBank() error {
	dbmu.Lock()
	defer dbmu.Unlock()
	if _, err := db.Exec("DELETE FROM question;"); err != nil {
		return err
	}
	nextQID = 1
	return os.RemoveAll(songsDirBase())
}

// applyPack 按当前方式导入资源包.
// merge=false 表示覆盖式导入(已清空题库).
// 返回 新增/替换/跳过 数量.
func applyPack(ctx *zero.Ctx, zipPath string, man *packManifest, overwrite bool) (int, int, int, error) {
	newCount, replaced, skipped := 0, 0, 0
	var conflicts []struct {
		item  packItem
		exist *Question
	}
	if !overwrite {
		// 先找出冲突项(以 难度+歌名+歌手 为键)
		for _, it := range man.Items {
			if it.Difficulty == "" || it.Title == "" {
				skipped++
				continue
			}
			if q, ok := findQuestionByKey(it.Difficulty, it.Title, it.Artist); ok {
				conflicts = append(conflicts, struct {
					item  packItem
					exist *Question
				}{it, q})
			}
		}
	}
	bulk := ""
	for i, cf := range conflicts {
		if bulk == "" {
			ctx.SendChain(message.Text(fmt.Sprintf(
				"检测到 %d 道题与现有题库冲突, 逐条确认是否用新包替换:\n\n现有: %s\n新包: %s\n回复 是/否 (可回复「全部替换」/「全部跳过」/「取消」)",
				len(conflicts), cf.exist.caption(), captionOf(&cf.item))))
		} else {
			ctx.SendChain(message.Text(fmt.Sprintf("[%d/%d]\n现有: %s\n新包: %s\n回复 是/否 继续确认~",
				i+1, len(conflicts), cf.exist.caption(), captionOf(&cf.item))))
		}
		switch awaitMergeDecision(ctx, bulk) {
		case "replace":
			if err := importItem(ctx, zipPath, &cf.item, cf.exist.ID); err != nil {
				return newCount, replaced, skipped, err
			}
			replaced++
			if err := pruneOrphanAudio(cf.exist); err != nil {
				return newCount, replaced, skipped, err
			}
		case "skip":
			skipped++
		case "allreplace":
			bulk = "replace"
		case "allskip":
			bulk = "skip"
		case "abort":
			return newCount, replaced, skipped, fmt.Errorf("导入已取消")
		}
	}
	// 非冲突项全部导入
	for _, it := range man.Items {
		if it.Difficulty == "" || it.Title == "" {
			skipped++
			continue
		}
		if !overwrite {
			if _, ok := findQuestionByKey(it.Difficulty, it.Title, it.Artist); ok {
				continue // 冲突项已在上面处理
			}
		}
		if err := importItem(ctx, zipPath, &it, 0); err != nil {
			return newCount, replaced, skipped, err
		}
		newCount++
	}
	return newCount, replaced, skipped, nil
}

// importItem 导入单条题目: 拷贝音频并写入数据库.
func importItem(ctx *zero.Ctx, zipPath string, it *packItem, keepID int64) error {
	// 拷贝音频
	audioData, err := readZipEntry(zipPath, it.Audio)
	if err != nil {
		return fmt.Errorf("题目 %s 缺少音频 %s: %v", captionOf(it), it.Audio, err)
	}
	diff := strings.ToUpper(strings.TrimSpace(it.Difficulty))
	fname := filepath.Base(it.Audio)
	dir := songsDirBase() + diff + "/"
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	if err := os.WriteFile(dir+fname, audioData, 0644); err != nil {
		return err
	}
	// 写入数据库
	q := &Question{
		Difficulty: diff,
		Title:      strings.TrimSpace(it.Title),
		Artist:     strings.TrimSpace(it.Artist),
		Source:     strings.TrimSpace(it.Source),
		Album:      strings.TrimSpace(it.Album),
		Audio:      fname,
		AudioMD5:   sumMD5(audioData),
		Enabled:    true,
	}
	if keepID != 0 {
		q.ID = keepID
	}
	return insertQuestion(q)
}

// pruneOrphanAudio 删除被替换旧题目引用、且无他人引用的音频.
func pruneOrphanAudio(old *Question) error {
	usages, err := loadQuestionsByDifficulty(old.Difficulty)
	if err != nil {
		return err
	}
	for _, u := range usages {
		if u.ID != old.ID && u.Audio == old.Audio {
			return nil // 仍有引用, 不删
		}
	}
	_ = os.Remove(songsDirBase() + old.Difficulty + "/" + old.Audio)
	return nil
}

// awaitMergeDecision 等待逐条确认. bulk 为批量方向(""/replace/skip).
func awaitMergeDecision(ctx *zero.Ctx, bulk string) string {
	if bulk == "replace" || bulk == "skip" {
		return bulk
	}
	next := zero.NewFutureEvent("message", 999, false, zero.OnlyGroup, ctx.CheckSession())
	recv, cancel := next.Repeat()
	defer cancel()
	timer := time.NewTimer(90 * time.Second)
	for {
		select {
		case <-timer.C:
			return "abort"
		case c := <-recv:
			s := strings.TrimSpace(c.Event.Message.String())
			switch s {
			case "是", "替换", "y", "Y":
				return "replace"
			case "否", "跳过", "n", "N":
				return "skip"
			case "全部替换":
				return "allreplace"
			case "全部跳过", "全部否":
				return "allskip"
			case "取消":
				return "abort"
			default:
				ctx.SendChain(message.Text("请回复 是/否/全部替换/全部跳过/取消~"))
			}
		}
	}
}

// captionOf 资源包条目摘要.
func captionOf(it *packItem) string {
	return fmt.Sprintf("[%s] 《%s》 - %s", it.Difficulty, it.Title, it.Artist)
}

// copyFile 复制文件.
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}

// md5FileHex 计算文件 md5.
func md5FileHex(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return sumMD5(data)
}

func sumMD5(data []byte) string {
	sum := md5.Sum(data)
	return hex.EncodeToString(sum[:])
}
