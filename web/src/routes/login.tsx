import { createFileRoute, redirect } from "@tanstack/react-router";
import { z } from "zod";

import { getMe } from "../api/gen/sdk.gen";
import { LoginForm } from "../features/auth/login-form";

const loginSearchSchema = z.object({
  redirect: z.string().optional(),
});

export const Route = createFileRoute("/login")({
  validateSearch: loginSearchSchema,
  beforeLoad: async () => {
    // Already authenticated tabs skip the login screen entirely.
    const result = await getMe();
    if (result.response?.ok) {
      throw redirect({ to: "/" });
    }
  },
  component: LoginPage,
});

function LoginPage() {
  return (
    <main className="flex h-dvh items-center justify-center bg-well">
      <div className="flex w-full max-w-sm flex-col gap-6">
        <h1 className="text-center text-lg font-semibold">Loomtale Studio</h1>
        <LoginForm />
      </div>
    </main>
  );
}
