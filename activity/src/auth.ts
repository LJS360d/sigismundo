import { DiscordSDK } from "@discord/embedded-app-sdk";

export type User = { id: string; name: string; avatar?: string };

export type Auth = {
  token: string;
  user: User;
  guildId: string;
  channelId: string;
  openExternal: (url: string) => void;
};

// Discord loads Activities with frame_id in the query; anything else is a
// plain browser, which only works with the server's DEV_AUTH.
export const inDiscord = new URLSearchParams(location.search).has("frame_id");

export async function authenticate(): Promise<Auth> {
  return inDiscord ? discordAuth() : devAuth();
}

async function discordAuth(): Promise<Auth> {
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
  const { access_token, token, user } = await res.json();
  await sdk.commands.authenticate({ access_token });
  return {
    token,
    user,
    guildId: sdk.guildId ?? "",
    channelId: sdk.channelId ?? "",
    openExternal: (url) => void sdk.commands.openExternalLink({ url }),
  };
}

async function devAuth(): Promise<Auth> {
  const q = new URLSearchParams(location.search);
  const params = new URLSearchParams({ id: q.get("user") ?? "1", name: q.get("name") ?? "" });
  const { token, user } = await getJSON<{ token: string; user: User }>(`/api/dev-token?${params}`);
  return {
    token,
    user,
    guildId: q.get("guild") ?? "dev",
    channelId: q.get("channel") ?? "dev",
    openExternal: (url) => void window.open(url, "_blank", "noopener"),
  };
}

async function getJSON<T>(url: string): Promise<T> {
  const res = await fetch(url);
  if (!res.ok) throw new Error(`${url}: ${res.status} ${await res.text()}`);
  return res.json();
}
