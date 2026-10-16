# Git

The Git view (`4`) is for seeing the state of every repository your services live in; the heavy lifting
is [lazygit](https://github.com/jesseduffield/lazygit)'s. Each service's working directory is looked up, the
project folder's own repo is included if it has one, and every repo appears once.

The **Repos** table on top has a row per repo: a mark, a dot (green clean, orange with changes), name, branch
(yellow when it differs from the most common one, which the title sums up: `main ×3 · feat/login ×1`),
ahead/behind or `no upstream`, `✎` changed files, `⚑` stashes, last commit, the services living there and
the outcome of the last action on it (`✓ pulled`, `✗ <git's message>`). Below, the selected repo: **Changes**,
**Branches** (with tracking and age), the commit **Graph** and **Stash** on the left, a read-only **Diff** (the
biggest panel) and the **Commands** log on the right; the diff follows whatever is selected (a file, a branch against `HEAD`, a commit, a stash). The view
opens on the repo of the service you had selected and refreshes every few seconds and after each action.
Pull is fast-forward only, and network operations never ask for a password (they fail instead; use an
agent or a credential helper).

Multi-repo actions work on the **marked** repos (`space` in the table; `a` marks all / none), or on all of
them when none is marked. Press `m`, then the action; they run at the same time, a toast sums them up and
each row shows its own result. Pull, push, new branch and switch branch ask first, listing the repos.

| Panel | Keys |
| --- | --- |
| everywhere | `L` open the selected repo in lazygit (orbit resumes when you quit it) · `f` `p` `u` fetch / pull / push the selected repo · `m` multi-repo mode · `j k` move · `g G` first / last · `z` zoom |
| Repos | `j k` pick the repo · `space` mark · `a` mark all / none · `enter` go to its changes (like `tab`) · `L` lazygit |
| multi-repo (`m`, then…) | `f` fetch · `p` pull · `u` push · `b` new branch (asks for the name) · `s` switch to a branch (asks for the name; repos without it report the error) · `esc` cancel |
| Changes | shown as a tree (folders first; a folder holding only another folder is merged: `src/ui/views`) · `space` stage / unstage the file, or everything under the folder · `enter` open the diff, or fold / unfold the folder · `a` stage / unstage all · `c` commit · `v` staged ⇄ unstaged side · `y` / `Y` copy the path (relative / absolute) |
| Branches | `enter` switch · `n` new branch (this repo) · `y` copy the name |
| Graph, Stash | `j k` pick one, the diff follows · `enter` focus the diff · `y` copy the hash (Graph) or ref (Stash) · `Y` hash and subject / stash message |
| Commands | `j k` move · `y` copy the command · `c` clear · `G` follow the latest. Lists every git command orbit ran for you (stage, commit, fetch, pull, push, branches), per repo, with ✓/✗, duration and git's message; the polling reads are not listed |
| Diff | `esc` back · `{` `}` previous / next file in Changes (folders skipped) · `[` `]` previous / next hunk · `y` / `Y` copy the path · `v` copy mode: a cursor appears (`j k` move), `v` again starts a range, `y` copies the line or range with its `+`/`-`, `esc` exits (unified view only) · `j k` `ctrl+u/d` `g G` scroll · `s` side-by-side (mouse wheel scrolls) |

Commit messages are one line; amend, discard, stash, hunk staging, branch deletion and the like live in lazygit.
