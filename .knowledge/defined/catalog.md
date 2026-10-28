# Catalog

`<GAMES_DIR>/catalog.yaml` (override with `CATALOG_PATH`), a list of entries. Mounted read-only with the games; loaded once at start. Entries whose `exe` doesn't exist are skipped (logged), so one manifest can list everything and you install a subset.

```yaml
- id: jpp7                         # required, unique, no '/'
  name: The Jackbox Party Pack 7   # defaults to id
  exe: jpp7/The Jackbox Party Pack 7.exe   # required; relative to the catalog's dir, or absolute
  args: []                         # extra args to the exe
  join_url: https://jackbox.tv/    # where players join; {code} is replaced with the room code
  nav_delay: 20s                   # wait before typing a nav macro (default 15s)
  games:                           # omit for a standalone game
    - id: quiplash3
      name: Quiplash 3
      nav: [Down, Return]          # keys from the pack menu to the game; omit = stop at the menu
    - id: devils
      name: The Devils and the Details
      nav: [Down, Down, wait:1s, Return]

- id: drawful2
  name: Drawful 2
  exe: /games/drawful2/Drawful 2.exe
```

- Pick keys are `entry` (launch, stop at the pack menu) or `entry/game` (launch, then run `nav`).
- `nav` steps are X keysym names: `Return`, `Escape`, `Up`/`Down`/`Left`/`Right`, `space`, `Tab`, `BackSpace`, single letters/digits, `F1`–`F12` (full list: `named` in `internal/input/input.go`), or `wait:<duration>`. Steps are 400ms apart.
- Macros are tuned by hand per pack: launch the pack menu, watch the stream, note the keys. A game without `nav` still works, the host picks it in the stream.
- `join_url` defaults to `https://jackbox.tv/`. Check whether your game's site accepts the code in the URL before using `{code}`.
