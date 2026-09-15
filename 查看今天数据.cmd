@echo off
chcp 65001 > nul
"D:\ScreenTimeObserver\collector-cli.exe" review %*
echo.
echo 上面能看到：今天的记录条数、时间跨度、空洞清单、疑似重复、时间去向。
echo 指定日期可以这样用：查看今天数据.cmd --date 2026-09-16
pause