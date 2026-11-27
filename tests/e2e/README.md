# Browser checks

Playwright checks that drive ZeroDesk in Chrome against an arm64 container standing in for a CardputerZero (a `pi` user, home folders and a small git project; no device hardware).

```bash
tests/e2e/run.sh                 # build, (re)start the container, run every check
tests/e2e/run.sh terminal t3     # only terminal.mjs, tests whose name starts with t3
```

`run.sh` builds the UI and the arm64 agent, builds the `zd-demo` image from `Dockerfile` the first time, copies the new binary into the `zd-demo` container (port 18700) and restarts it. Needs Docker, Go, Node and Google Chrome (`channel: 'chrome'`). Set `ZD_E2E_SKIP_BUILD=1` to test the binary already in the container.

| File | Covers |
|---|---|
| `split.mjs` | splitting and closing panes keeps the other panes' shells and scrollback |
| `terminal.mjs` | tab and pane closing and where the keyboard goes, the find bar (count, highlights, Next/Previous from a selection), fit of the last row, closing a pane while xterm loads, ⌘D from the find bar |

Each test prints `PASS` or `FAIL` with what it saw; the exit code is non-zero if any failed. Checks that a fix depends on were first run against the build before the fix and seen to fail.

Not covered here: DeviceApp and Voice Memos replies that arrive after their window closed (they need controlled device responses), and anything on real hardware.
