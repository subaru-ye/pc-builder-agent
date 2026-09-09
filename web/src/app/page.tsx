import { HomeWorkspace } from "@/components/home-workspace";

export default function HomePage() {
  return <HomeWorkspace showEvaldesk={!!process.env.EVALDESK_API_BASE_URL} />;
}
