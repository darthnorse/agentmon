import { useNavigate } from "@tanstack/react-router";
import { LoginForm } from "@/components/LoginForm";

export function LoginRoute() {
  const navigate = useNavigate();
  return <LoginForm onSuccess={() => navigate({ to: "/" })} />;
}
