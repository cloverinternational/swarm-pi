Output manual_tests/vhs/output/sidebar-tool-render.mp4

Set Shell "bash"
Set FontFamily "DejaVu Sans Mono"
Set FontSize 14
Set Width 1600
Set Height 1020
Set Theme "Catppuccin Mocha"
Set Padding 0
Set TypingSpeed 1ms

Sleep 500ms
Type "./manual_tests/vhs/toolrender-fixture"
Enter
Sleep 12s
Screenshot manual_tests/vhs/output/sidebar-tools-state-a.png
Sleep 15s
Screenshot manual_tests/vhs/output/sidebar-tools-state-d.png
Sleep 500ms
Ctrl+C
Sleep 500ms
