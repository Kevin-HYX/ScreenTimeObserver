# ScreenTimeObserver

Windows 常驻屏幕时间采集器。登录后自动运行，按固定间隔记录前台窗口与空闲状态，数据只写入本机 JSONL，供后续统计与 AI 读取。

## 功能

- 事件驱动采集前台窗口（SetWinEventHook 监听前台切换与还原），外加 30 秒低频对账兜底遗漏。
- 每秒采样一次系统空闲时间（GetLastInputInfo），超过 180 秒判定为空闲。
- 疑似空闲时再查一次默认播放设备的音量：有声音说明人在看视频或在听东西，记成 media，不算空闲；原值 idle_sec 始终保留。
- 区分 active / idle / locked / suspended / paused 状态，锁屏、休眠、暂停都会留下明确记录。
- 托盘常驻：暂停与恢复采集、打开数据目录、复制数据目录路径、查看状态、退出；Explorer 重启后自动重新注册图标。
- 托盘图标随状态变色变形：蓝色时钟记录中、灰色时钟空闲、琥珀暂停条已暂停、红色感叹号采集异常。
- 按日切分写入 data/YYYY-MM-DD.jsonl，每行一条 JSON，可直接读取。
- 双保险自启：计划任务 ScreenTimeObserver-M1（登录触发）加当前用户 Run 启动项；单实例互斥避免重复运行。

## 目录

| 路径 | 说明 |
|------|------|
| src/ | Go 源码，仅依赖标准库 |
| assets/ | 应用与托盘图标，SVG 为源文件，ICO 与 PNG 由脚本生成 |
| docs/ | 部署说明、开源方案对比、代码审查记录 |
| config.json | 运行配置，缺项自动回退默认值 |
| data/ | 采集数据，本机保留，不纳入版本控制 |
| logs/ | 运行日志与状态快照，不纳入版本控制 |

## 构建

```
cmd /c D:\ScreenTimeObserver\src\build.cmd
```

产出 collector.exe（GUI 子系统，无控制台窗口）与 collector-cli.exe（命令行，用于状态与日报）。

图标重建需要 Python 与 cairosvg、Pillow：

```
python D:\ScreenTimeObserver\src\build-icons.py
```

## 日常使用

```
查看状态.cmd                          采集器状态、前台应用、开机自启检查
查看今天数据.cmd                      当日复看：记录条数、时间跨度、空洞、重复
查看今天数据.cmd --date 2026-09-16    指定日期
```

命令行等价形式：collector-cli.exe status / review / pause / resume。

## 测试

```
cd src
go test ./...
go vet ./...
```

## 数据与隐私

采集数据只保存在本机 data/ 目录，不写入代码仓库，也不上传云端。窗口标题按配置截断到 title_max_len，当前不做脱敏。

## 已知限制

- 首次部署后需要一次真实重启登录来验收自启链路。
- 锁屏状态通过输入桌面可访问性推断，未注册 WTS 会话通知，最长约 30 秒才被对账纠正。
- 断电等异常退出可能在 JSONL 末尾留下半行，目前没有自动隔离机制。
