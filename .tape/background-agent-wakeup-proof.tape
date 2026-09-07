Require tmux
Require swarm
Output .tape/background-agent-wakeup-proof.gif
Output .tape/background-agent-wakeup-proof.mp4
Set Shell "bash"
Set FontSize 14
Set Width 1200
Set Height 700
Set Padding 20
Set Margin 30
Set MarginFill "#000000"

Hide
Type "tmux kill-session -t pi-wakeup-proof 2>/dev/null || true"
Enter
Type "bash /home/swarm/Work/Pi-Swarm/.tape/background-agent-wakeup-proof.sh"
Enter
Sleep 2s
Show
Sleep 60s
Hide
Type "tmux capture-pane -p -t pi-wakeup-proof -S -200"
Enter
Sleep 3s
Type "tmux kill-session -t pi-wakeup-proof"
Enter
Sleep 1s
