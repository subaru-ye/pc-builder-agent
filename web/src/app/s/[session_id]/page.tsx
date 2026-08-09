import { SessionWorkspace } from "@/components/session-workspace";

export default async function SessionPage({ params }: { params: Promise<{ session_id: string }> }) {
  const { session_id } = await params;
  return <SessionWorkspace sessionID={session_id} />;
}
