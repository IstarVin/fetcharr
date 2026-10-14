#!/bin/sh
# Test helper: ignores SIGTERM (so it can only be removed by SIGKILL) and
# spawns a long-lived grandchild that inherits the same disposition.
# usage: spawn-child.sh PIDFILE   (the grandchild's pid is written to PIDFILE)
trap '' TERM
sleep 300 &
echo $! > "$1"
echo "[helper] started"
wait
