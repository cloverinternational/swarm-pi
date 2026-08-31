Output manual_tests/vhs/output/bash-tool-render-after.mp4

Set Shell "bash"
Set FontFamily "DejaVu Sans Mono"
Set FontSize 14
Set Width 1600
Set Height 1020
Set Theme "Catppuccin Mocha"
Set Padding 0
Set TypingSpeed 20ms

Sleep 500ms
Type "./swarm --debug-render"
Enter
Sleep 2s
Type "2"
Sleep 1s
Screenshot manual_tests/vhs/output/bash-tool-render-short-after.png
Type "0"
Sleep 300ms
Type "3"
Sleep 1s
Screenshot manual_tests/vhs/output/bash-tool-render-long-after.png
Sleep 500ms
Type "q"
Sleep 500ms
