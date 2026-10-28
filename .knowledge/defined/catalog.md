# Catalog

`<GAMES_DIR>/catalog.yaml` (override with `CATALOG_PATH`), a list of entries. Mounted read-only with the games; loaded once at start. Entries whose `exe` doesn't exist are skipped (logged), so one manifest can list everything and you install a subset.

```yaml
- id: jpp7                         # required, unique, no '/'
  name: The Jackbox Party Pack 7   # defaults to id
  description: |                   # shown on the card above Launch; newlines kept
    Quiplash 3
    Talking Points
  exe: jpp7/The Jackbox Party Pack 7.exe   # required; relative to the catalog's dir, or absolute
  image: jpp7/cover.png            # card cover, served at /api/catalog/<id>/image
  args: []                         # extra args to the exe

- id: drawful2
  name: Drawful 2
  exe: /games/drawful2/Drawful 2.exe
```

- One Launch button per entry; packs open on their own menu, the controller picks the game in the stream.
- `exe` and `image` paths are relative to the catalog's dir, or absolute. Images go through the API since Discord's CSP blocks external URLs.
- Install each pack flat in its dir (exe at `jpp7/<name>.exe`). Bundled `_Redist` installers aren't needed: packs ship their VC++ runtimes next to the exe, and D3D11 renders through DXVK on Mesa lavapipe (in the image, copied into the prefix each launch; wined3d rendered JPP7 black).
- The games mount is read-only: Steam-emu builds need `SaveType = 1` in `SteamConfig.ini` so saves land in the Wine prefix under `DATA_DIR`.
