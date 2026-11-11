#!/bin/sh
# Makes a fresh toy repo and a scratch home for demo.tape, in /tmp, so the
# recording shows no real workspace.
set -e
D=/tmp/nhdemo
rm -rf "$D"
mkdir -p "$D/home" "$D/todo"
cat > "$D/home/init.lua" <<'EOF'
require("neoharness").setup({ mcp_port = 47998 })
require("neoharness.claude").setup({ model = "haiku" })
EOF
cat > "$D/todo/todo.py" <<'EOF'
"""A tiny todo list kept in todo.txt."""
import sys

FILE = "todo.txt"


def load():
    try:
        with open(FILE) as f:
            return [line.rstrip("\n") for line in f]
    except FileNotFoundError:
        return []


def save(items):
    with open(FILE, "w") as f:
        f.write("".join(i + "\n" for i in items))


def main(args):
    items = load()
    if not args or args[0] == "list":
        for n, item in enumerate(items, 1):
            print(f"{n}. {item}")
    elif args[0] == "add":
        items.append(" ".join(args[1:]))
        save(items)
    elif args[0] == "done":
        items.pop(int(args[1]) - 1)
        save(items)


if __name__ == "__main__":
    main(sys.argv[1:])
EOF
cd "$D/todo"
git init -q
git add .
git -c user.name=demo -c user.email=demo@example.com commit -qm init
