# -*- coding: utf-8 -*-
"""将浅羽猜歌源数据转换为插件资源包.

源数据目录通常包含:
  - 说明.txt   (玩法说明)
  - *.xlsx     (官方题库: 时间/歌名/来源/歌手/专辑)
  - WY-11/     (音频片段目录, 文件名编码了难度[A/B/C]-歌手-歌名-出处-片段时长)

输出(不纳入 git, 由管理员自行导入):
  /tmp/opencode/asaba/resource_pack/   资源包目录(manifest.json + audios/)
  /tmp/opencode/asaba/resource_pack.zip 可直接「浅羽导入歌包」的 zip

用法:
  python3 build_pack.py          # 依据脚本开头的 SRC 目录生成
依赖: 无(仅标准库; 读取 xlsx 需先在脚本内开启 openpyxl 分支或使用 /tmp/make_pack_extract.py)
"""
import json, os, re, sys, unicodedata, zipfile
from datetime import datetime

SRC = sys.argv[1] if len(sys.argv) > 1 else '/tmp/opencode/caige/猜歌'
AUD = os.path.join(SRC, 'WY-11')
OUT = '/tmp/opencode/asaba/resource_pack'

POOL = json.load(open('/tmp/xlsx_pool.json', encoding='utf-8'))

def norm(s):
    if not s:
        return ''
    s = s.replace('\u00a0', ' ')
    # 全角->半角
    s = unicodedata.normalize('NFKC', s)
    s = s.translate(str.maketrans({'’': "'", '‘': "'", '“': '"', '”': '"', '☆': '', '♪': ''}))
    s = re.sub(r'[\s\-_~:：;；,，.。!！?？()（）\[\]【】<>《》|/\\]+', '', s)
    return s.lower()

def pool_time(v):
    # "1'1\"" -> "1'1"; "44\"" -> "44"
    m = re.match(r"(\d+'(?:\d+)?|\d+)\"?", v or '')
    if not m:
        return ''
    return norm(m.group(1)).replace("'", '')

def file_time(name):
    # "1'13" / "44" / "2'41" / "1'15两种" / "1;34" / "1’43" / "1'8"
    base = name[:-4]
    m = re.search(r"[-·](\d+(?:['’;:]\d+)?)(?:两种|\.\.\.)?$", base)
    if not m:
        return ''
    t = m.group(1).replace('’', "'").replace(';', "'").replace('：', "'")
    if ':' in t: t = t.replace(':', "'")
    return norm(t).replace("'", '')

pool_by_time = {}
for r in POOL:
    t = pool_time(r.get('A'))
    pool_by_time.setdefault(t, []).append(r)

def candidates(f, cands):
    return [(c.get('D'), c.get('B'), c.get('C')) for c in cands[:4]]

# 预处理音频文件
files = sorted(f for f in os.listdir(AUD) if f.endswith('.mp3'))
items = []
unmatched = []

def lcp(a, b):
    n = 0
    for x, y in zip(a, b):
        if x != y:
            break
        n += 1
    return n

def longest_title_prefix(title, blob):
    best = 0
    for i in range(len(blob)):
        best = max(best, lcp(title, blob[i:]))
    return best

for f in files:
    m = re.match(r'^([A-Za-z]+)\s*-\s*(.+)$', f[:-4])
    diff = (m.group(1) if m else '').upper()
    start = m.group(2) if m else f[:-4]
    if diff.startswith('A'):
        diff = 'A'
    elif diff.startswith('B'):
        diff = 'B'
    elif diff.startswith('C'):
        diff = 'C'
    else:
        diff = 'A'
    blob = norm(start)
    best = None
    best_score = 0
    for r in POOL:
        ti = norm(r.get('B'))
        ar = norm(r.get('D'))
        so = norm(r.get('C'))
        if not ti:
            continue
        mlen = longest_title_prefix(ti, blob)
        if mlen < 3:
            continue
        score = mlen
        if ar and mlen < len(ti) and len(ar) >= 2 and blob.startswith(ar[:2]):
            score += 2
        if so and so in blob:
            score += 2
        if score > best_score:
            best_score = score
            best = r
    if best and best_score >= 3:
        items.append({
            'difficulty': diff,
            'artist': best.get('D') or '',
            'title': best.get('B') or '',
            'source': best.get('C') or '',
            'album': best.get('E') or '',
            'audio': f,
            'time': '',
        })
    else:
        unmatched.append((f, diff, '', [(c.get('D'), c.get('B'), c.get('C')) for c in POOL[:0]]))

# 手工修正: 文件名与题库存在歧义/错字/简繁不一致/时间不符的题目
OVERRIDES = {
    '-B-初音未来-猫耳开关-44.mp3': dict(difficulty='B'),
    "B-TRUE- Sincerely-紫罗兰-1'16.mp3": dict(difficulty='B', artist='TRUE', title='Sincerely', source='紫罗兰的永恒花园'),
    "B-初音未来 - 脳漿炸裂ガー-1'9.mp3": dict(artist='初音ミク', title='脳漿炸裂ガール'),
    "B-初音未来- 妄想税-1'27.mp3": dict(artist='初音ミク', title='妄想税'),
    'B-初音未来-だんだん早-18.mp3': dict(artist='初音ミク', title='だんだん早くなる'),
    "C-Yunomi-インドア系な-インドア系な-1'20.mp3": dict(difficulty='C', artist='Yunomi', title='インドア系ならトラックメイカー', album='インドア系ならトラックメイカー'),
    'C-松下優也 - Bird-黑执事.mp3': dict(artist='松下優也', title='Bird', source='黑执事'),
    # 简繁/全半角差异导致的未匹配项
    "A- 雨宮天-奏〈かなで-一周的朋友-56.mp3": dict(artist='雨宮天', title='奏 (かなで)', source='一周的朋友'),
    'A-JINDOU- 快晴・上昇-游戏王-40.mp3': dict(artist='JINDOU', title='快晴·上升·ハレルーヤ', source='游戏王'),
    "A-Suara- 夢想歌-传颂之物-1'4.mp3": dict(artist='Suara', title='梦想歌', source='传颂之物'),
    "A-やなぎなぎ- 終わりの世界か-1'43.mp3": dict(artist='やなぎなぎ', title='终わりの世界から'),
    "A-ウルトラタワ-希望の呗-食戟之灵-1'29.mp3": dict(artist='ウルトラタワー', title='希望之歌', source='食戟之灵'),
    'A-タニザワトモフミ- 爽風-好想告诉你-29.mp3': dict(artist='タニザワトモフミ', title='爽风', source='好想告诉你'),
    "A-月宮みどり- 素顔-这个是僵尸吗-1\u201943.mp3": dict(artist='月宮みどり', title='素颜', source='这个是僵尸吗'),
    'B-KARAKURI-B.A.A.B.-H-A-J-I---46.mp3': dict(artist='KARAKURI', title='B.A.A.B. -H-A-J-I-'),
    "A-TRUE-Another colony-史莱姆-1'22.mp3": dict(artist='TRUE', title='Another colony', source='关於我转生变成史莱姆这档事'),
    'B-高田憂希合唱-SAKURA-new game-1\'25.mp3': dict(artist='高田憂希'),
}
# 未自动匹配的题目必须位于 overrides, 有则并入 items.
for f, diff, _ft, _cs in unmatched:
    if f not in OVERRIDES:
        print('ERROR: 未匹配且无人工修正:', f)
        sys.exit(2)
    items.append({'difficulty': None, 'artist': '', 'title': '', 'source': '', 'album': '',
                  'audio': f})

for it in items:
    if it['audio'] in OVERRIDES:
        it.update(OVERRIDES[it['audio']])
    it['difficulty'] = it['difficulty'] or 'A'
    for k in ('artist', 'title', 'source', 'album'):
        it[k] = (it[k] or '').strip()
    it['difficulty'] = it['difficulty'].strip()

# 预览全部结果
for it in sorted(items, key=lambda x: (x['difficulty'], x['audio'])):
    print('%-3s %-16s | %s / %s / %s' % (it['difficulty'], it['audio'][:18], it['artist'], it['title'], it['source']))

print('\nMATCHED %d / %d' % (len(items), len(files)))

# ---------------- 校验: 文件名首段(编码歌手)与题库歌手一致性 ----------------
def first_seg(fname):
    rest = re.sub(r'^[A-Za-z]+\s*-', '', fname[:-4])
    seg = rest.split('-')[0].strip()
    return seg

warn = []
for it in items:
    seg = norm(first_seg(it['audio']))
    ar = norm(it['artist'])
    if not ar:
        warn.append((it['audio'], '无歌手', seg))
        continue
    if len(seg) < 2:
        continue
    # 编码段必须在题库歌手里, 或题库歌手在编码段里(容忍简繁/略写)
    if seg not in ar and ar not in seg and not (it['audio'] in OVERRIDES):
        warn.append((it['audio'], it['artist'], seg))
print('ARTIST-WARN %d' % len(warn))
for a in warn:
    print('  ', a)

# ---------------- 落盘: 拷贝音频 + 写 manifest + 打包 zip ----------------
os.makedirs(OUT, exist_ok=True)
manifest = {
    'name': '浅羽猜歌官方歌曲包',
    'description': '源自浅羽猜歌(AsabaGuessingGame)WY-11歌曲目录与官方题库 xlsx, 共 %d 题.' % len(items),
    'format': 1,
    'exported_at': datetime.now().strftime('%Y-%m-%d %H:%M:%S'),
    'items': [],
}
added = []
for it in sorted(items, key=lambda x: (x['difficulty'], x['audio'])):
    name = it['audio']
    src = os.path.join(AUD, it['audio'])
    rel = 'audios/%s/%s' % (it['difficulty'], name)
    added.append((src, rel, it['difficulty']))
    manifest['items'].append({
        'difficulty': it['difficulty'],
        'title': it['title'],
        'artist': it['artist'],
        'source': it['source'],
        'album': it['album'],
        'audio': rel,
    })

with open(os.path.join(OUT, 'manifest.json'), 'w', encoding='utf-8') as f:
    json.dump(manifest, f, ensure_ascii=False, indent=2)
print('manifest.json:', len(manifest['items']), 'items')

from collections import Counter
print('difficulty:', Counter(m['difficulty'] for m in manifest['items']))

zip_path = '/tmp/opencode/asaba/resource_pack.zip'
with zipfile.ZipFile(zip_path, 'w', zipfile.ZIP_STORED) as z:
    z.write(os.path.join(OUT, 'manifest.json'), 'manifest.json')
    for src, rel, _d in added:
        z.write(src, rel)
print('zip:', zip_path, os.path.getsize(zip_path), 'bytes')
if unmatched:
    print('STILL UNMATCHED (%d):' % len(unmatched))
    for f, d, ft, cs in unmatched:
        print('  [%s] %s (time=%s)' % (d, f, ft))