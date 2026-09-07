# Record a real Pi TUI continuously; no synthetic follow-up input is sent.
Require pi
Require tmux
Output .tape/swarm-goal-proof.gif
Output .tape/swarm-goal-proof.mp4
Set Shell "bash"
Set Width 1400
Set Height 900
Set FontSize 14
Set Padding 20
Set MarginFill "#000000"
Hide
Type "bash .tape/swarm-goal-proof.sh"
Enter
Sleep 2s
Show
Sleep 80s
Screenshot .tape/swarm-goal-proof.png
# Leave Pi alive for direct inspection; VHS closes its client, not the server.
