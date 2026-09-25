import { useNavigate, useSearch } from "@tanstack/react-router";
import { useState } from "react";
import { z } from "zod";

import { ApiError } from "../../api/client";
import { Button } from "../../components/ui/button";
import { Input } from "../../components/ui/input";
import { useLogin } from "./use-auth";

const loginSchema = z.object({
  email: z.string().email("Enter a valid email address"),
  password: z.string().min(1, "Password is required"),
});

export function LoginForm() {
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [fieldErrors, setFieldErrors] = useState<{ email?: string; password?: string }>({});
  const login = useLogin();
  const navigate = useNavigate();
  const search = useSearch({ strict: false }) as { redirect?: string };

  function onSubmit(event: React.FormEvent) {
    event.preventDefault();
    const result = loginSchema.safeParse({ email, password });
    if (!result.success) {
      const flattened = result.error.flatten().fieldErrors;
      setFieldErrors({ email: flattened.email?.[0], password: flattened.password?.[0] });
      return;
    }
    setFieldErrors({});
    login.mutate(result.data, {
      onSuccess: () => {
        void navigate({ to: search.redirect ?? "/" });
      },
    });
  }

  const apiError = login.error instanceof ApiError ? login.error : undefined;

  return (
    <form onSubmit={onSubmit} className="flex w-full max-w-sm flex-col gap-4" noValidate>
      <div className="flex flex-col gap-1.5">
        <label htmlFor="email" className="text-sm text-text-2">
          Email
        </label>
        <Input
          id="email"
          type="email"
          autoComplete="username"
          value={email}
          onChange={(event) => setEmail(event.target.value)}
          aria-invalid={Boolean(fieldErrors.email)}
          aria-describedby={fieldErrors.email ? "email-error" : undefined}
        />
        {fieldErrors.email && (
          <p id="email-error" role="alert" className="text-xs text-destructive">
            {fieldErrors.email}
          </p>
        )}
      </div>
      <div className="flex flex-col gap-1.5">
        <label htmlFor="password" className="text-sm text-text-2">
          Password
        </label>
        <Input
          id="password"
          type="password"
          autoComplete="current-password"
          value={password}
          onChange={(event) => setPassword(event.target.value)}
          aria-invalid={Boolean(fieldErrors.password)}
          aria-describedby={fieldErrors.password ? "password-error" : undefined}
        />
        {fieldErrors.password && (
          <p id="password-error" role="alert" className="text-xs text-destructive">
            {fieldErrors.password}
          </p>
        )}
      </div>
      {apiError && (
        <p role="alert" className="text-sm text-destructive">
          {apiError.status === 401 ? "Incorrect email or password." : apiError.detail ?? apiError.title}
        </p>
      )}
      <Button type="submit" variant="primary" disabled={login.isPending}>
        {login.isPending ? "Signing in..." : "Sign in"}
      </Button>
    </form>
  );
}
