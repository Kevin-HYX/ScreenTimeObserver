@echo off
chcp 65001 > nul
"D:\ScreenTimeObserver\collector-cli.exe" status
echo.
echo 上面能看到：采集器是否在跑、是否暂停、前台是什么、以及「开机自启检查」。
pause