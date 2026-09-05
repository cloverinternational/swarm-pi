# Live Pi-Swarm acceptance recording.
# Shows the Ctrl+T thinking selector and the persistent TaskManage widget.

Require pi

Output .tape/pi-swarm-parity.gif
Output .tape/pi-swarm-parity.mp4

Set Shell "bash"
Set FontSize 14
Set Width 1280
Set Height 820
Set Padding 16
Set Margin 24
Set MarginFill "#000000"
Set TypingSpeed 1ms

Hide
Type "clear"
Enter
Show

Type "pi"
Enter
Sleep 5s

Ctrl+T
Sleep 3s
Escape
Sleep 1s

Type "Use TaskManage in one atomic call with exactly these operations: create Widget Active with status in_progress and active true; create Widget Blocked as pending and addBlockedBy the first using an in-batch reference. Do not use any other tool. Finish with exactly WIDGET_READY."
Enter
Sleep 30s

Screenshot .tape/pi-swarm-parity.png
Sleep 2s
