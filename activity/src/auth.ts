import { DiscordSDK } from "@discord/embedded-app-sdk";

// Returns the server token for /api/ws. Discord loads Activities with frame_id
// in the query; anything else is a plain browser, which only works with the
// server's DEV_AUTH.
export async function authenticate(): Promise<string> {
  const q = new URLSearchParams(location.search);
  if (!q.has("frame_id")) {
    const params = new URLSearchParams({ id: q.get("user") ?? "1", name: q.get("name") ?? "" });
    return (await getJSON<{ token: string }>(`/api/dev-token?${params}`)).token;
  }
  const { client_id } = await getJSON<{ client_id: string }>("/api/config");
  const sdk = new DiscordSDK(client_id);
  await sdk.ready();
  const { code } = await sdk.commands.authorize({
    client_id,
    response_type: "code",
    state: "",
    prompt: "none",
    scope: ["identify"],
  });
  const res = await fetch("/api/token", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ code }),
  });
  if (!res.ok) throw new Error(`token exchange failed: ${await res.text()}`);
  const { access_token, token } = await res.json();
  await sdk.commands.authenticate({ access_token });
  return token;
}

export async function getJSON<T>(url: string): Promise<T> {
  const res = await fetch(url);
  if (!res.ok) throw new Error(`${url}: ${res.status} ${await res.text()}`);
  return res.json();
}
