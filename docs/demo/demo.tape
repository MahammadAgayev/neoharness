# Records docs/demo/demo.gif with real agents on a toy repo in /tmp.
# Run from the repository root: vhs docs/demo/demo.tape

Output docs/demo/demo.gif

Set Shell zsh
Set FontSize 15
Set Width 1440
Set Height 820
Set Padding 16
Set Theme "rose-pine"
Set TypingSpeed 40ms

Hide
Type "sh docs/demo/setup.sh && export NEOHARNESS_HOME=/tmp/nhdemo/home && clear" Enter
Sleep 2s
Show

Type "neoharness" Enter
Sleep 2s
Type ":new todo /tmp/nhdemo/todo" Sleep 700ms Enter
Sleep 1.5s

Set TypingSpeed 15ms
Type "Improve todo.py in three threads: tests adds pytest tests, json adds a --json flag to list, priorities writes a short design note on task priorities as an artifact."
Sleep 800ms
Enter
Wait+Screen@60s /● priorities/
Sleep 4s

# Open a running thread from the picker, and its artifact once written.
Escape
Type " pt"
Sleep 1s
Type "prio"
Sleep 800ms
Enter
Wait+Screen@120s /◇ .*· artifact/
Sleep 3s
Type " pa"
Sleep 1.5s
Enter
Sleep 3s
Type "12j"
Sleep 3s
