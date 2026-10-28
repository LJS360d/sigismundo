# Design decisions (initial grilling, 2026-10-26)

- Video into the VC: Discord bots can't Go Live, a selfbot breaks ToS. → Discord Activity streams the game; the bot doesn't join voice at all.
- jackbox.tv inside the Activity (proxying through URL mappings) rejected as fragile → Play tab only opens jackbox.tv externally with the room code.
- Exes: launcher-free copies, everything included, provided by the deployer at a fixed path. No Steam/DRM handling.
- Catalog: hand-written manifest. Selecting a game: nav macro per game when present, else stop at the pack menu.
- Command: `/host [game]` (autocomplete) + paginated picker (page per pack). Leaves room for non-Jackbox games in the same catalog.
- One global session. Anyone in a VC may host; the Activity can also be opened standalone (picker inside). Other VCs/guilds join view-only.
- Settings: in-game options through the stream; stream/session settings as env defaults + `/settings` (owners).
- Audio inside the Activity stream (A/V sync), not via the bot in voice.
- Control: host + handoff, owners override. Input: mouse + keyboard.
- Stream tech: WebCodecs raw H.264 + Opus over WebSocket. 720p30 default.
- Room code: typed by the host (no OCR).
- Public HTTPS exposure: left to the deployer.
- Ends on: /stop or button, idle timeout, game exit; controller leaving the VC hands control on.
- Frontend: Vite + vanilla TS, embedded in the Go binary. One container image.
