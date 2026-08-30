export type AuthEvent = "login" | "logout" | "profile";

export function broadcastAuth(event: AuthEvent) {
  if (typeof BroadcastChannel === "undefined") return;
  const channel = new BroadcastChannel("pcb-auth");
  channel.postMessage(event);
  channel.close();
}
