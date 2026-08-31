# Model generation settings TUI preview.
# Opens the existing per-model editor and demonstrates an explicit temperature
# override without saving or changing the user's provider configuration.

Require "bash"

Output .tape/model-generation-settings.gif
Output .tape/model-generation-settings.mp4

Set Shell "bash"
Set FontSize 14
Set Width 1200
Set Height 720
Set Padding 12
Set Margin 20
Set MarginFill "#000000"

Hide
Type "/tmp/swarm-model-settings-preview --workspace /home/swarm/Work/mono"
Enter
Sleep 4s
Show

Type "/providers"
Enter
Sleep 1s
Tab
Down
Enter
Sleep 1s
Enter
Sleep 1s
Type "e"
Sleep 2s

# Move from Model ID to Temperature and select explicit 0.0.
Down
Down
Down
Down
Down
Down
Down
Right
Sleep 2s
Screenshot .tape/model-generation-settings.png

Escape
Sleep 500ms
Ctrl+C
Sleep 500ms
