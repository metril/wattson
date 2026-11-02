import { useEffect, useState } from 'react';
import { Navigate, useNavigate, useSearchParams } from 'react-router';
import { useQueryClient } from '@tanstack/react-query';
import { KeyRound } from 'lucide-react';
import { api, ApiError, unwrap } from '@/lib/api';
import { AUTH_INFO_KEY, ME_KEY, useAuthInfo } from '@/lib/auth';
import { errMessage } from '@/lib/errors';
import { Banner } from '@/components/Banner';
import { Button } from '@/components/Button';
import { Card } from '@/components/Card';
import { Field } from '@/components/Field';

const OIDC_ERRORS: Record<string, string> = {
  forbidden: 'Your account is not in an allowed group.',
  not_provisioned: 'No Wattson account is linked to this identity. Ask an admin.',
  inactive: 'This account is disabled.',
  state: 'Sign-in expired or was tampered with. Try again.',
  exchange_failed: 'The identity provider rejected the sign-in.',
  provider_error: 'The identity provider returned an error.',
  unknown_provider: 'That sign-in option no longer exists.',
  provider_unavailable: 'The identity provider is unreachable.',
};

/** Only same-origin paths: starts with "/" but not "//" or "/\". */
export function safeReturn(rt: string | null): string {
  return rt && /^\/(?![/\\])/.test(rt) ? rt : '/';
}

export function formatRetry(seconds: number): string {
  if (seconds >= 60) {
    const m = Math.ceil(seconds / 60);
    return `${m} minute${m === 1 ? '' : 's'}`;
  }
  return `${seconds} second${seconds === 1 ? '' : 's'}`;
}

export function LoginPage() {
  const [params] = useSearchParams();
  const returnTo = safeReturn(params.get('return_to'));
  const oidcError = params.get('error');
  const info = useAuthInfo();
  const qc = useQueryClient();
  const navigate = useNavigate();

  const [showLocal, setShowLocal] = useState<boolean | null>(null);
  const [username, setUsername] = useState('');
  const [password, setPassword] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  // Epoch ms until which the server refuses attempts (429 Retry-After).
  const [blockedUntil, setBlockedUntil] = useState(0);
  const [now, setNow] = useState(() => Date.now());

  useEffect(() => {
    if (blockedUntil <= Date.now()) return;
    const t = setInterval(() => {
      setNow(Date.now());
      if (Date.now() >= blockedUntil) clearInterval(t);
    }, 1000);
    return () => clearInterval(t);
  }, [blockedUntil]);
  const wait = Math.max(0, Math.ceil((blockedUntil - now) / 1000));

  if (info.isPending) {
    return (
      <p role="status" className="p-8 text-dim">
        Loading…
      </p>
    );
  }
  if (info.isError) {
    return (
      <div className="p-4">
        <Banner tone="error">Can't reach the Wattson server. Check the connection and reload.</Banner>
      </div>
    );
  }
  if (info.data.needs_setup) return <Navigate to="/setup" replace />;

  const { providers, local_login } = info.data;
  const localShown = showLocal ?? local_login;

  async function submit() {
    setBusy(true);
    setError(null);
    try {
      const r = await api.POST('/api/auth/login', { body: { username: username.trim(), password } });
      if (r.response.status === 429) {
        const secs = Math.max(1, Number(r.response.headers.get('Retry-After')) || 60);
        setNow(Date.now());
        setBlockedUntil(Date.now() + secs * 1000);
        return;
      }
      unwrap(r);
      qc.removeQueries({ queryKey: ME_KEY });
      qc.removeQueries({ queryKey: AUTH_INFO_KEY });
      navigate(returnTo, { replace: true });
    } catch (e) {
      if (e instanceof ApiError && e.code === 'local_login_disabled') {
        setError('Local login is disabled. Use a provider button.');
      } else if (e instanceof ApiError && e.code === 'invalid_credentials') {
        setError('Wrong username or password.');
      } else {
        setError(errMessage(e));
      }
    } finally {
      setBusy(false);
    }
  }

  const rateLimited = wait > 0;
  const message = rateLimited ? `Too many attempts. Try again in ${formatRetry(wait)}.` : error;
  const callbackError = oidcError ? (OIDC_ERRORS[oidcError] ?? 'Sign-in failed. Try again.') : null;

  return (
    <div className="mx-auto flex min-h-dvh max-w-md flex-col justify-center gap-4 p-4">
      <h1 className="numeral text-2xl">Sign in</h1>
      {callbackError && <Banner tone="error">{callbackError}</Banner>}
      <Card className="flex flex-col gap-4">
        {providers.map((p) => (
          <a
            key={p.slug}
            href={`${p.login_url}?return_to=${encodeURIComponent(returnTo)}`}
            className="inline-flex min-h-touch items-center justify-center gap-2 rounded-control bg-accent px-4 font-medium text-accent-ink hover:brightness-110"
          >
            <KeyRound aria-hidden className="size-5" />
            Sign in with {p.display_name}
          </a>
        ))}
        {providers.length > 0 && !localShown && (
          <p className="text-sm text-dim">Local sign-in is turned off, except for the setup admin.</p>
        )}
        {!localShown && (
          <Button onClick={() => setShowLocal(true)}>Sign in with a local account</Button>
        )}
        {localShown && (
          <form
            className="flex flex-col gap-4"
            onSubmit={(e) => {
              e.preventDefault();
              void submit();
            }}
          >
            <Field
              label="Username"
              autoComplete="username"
              autoCapitalize="none"
              value={username}
              onChange={(e) => setUsername(e.target.value)}
            />
            <Field
              label="Password"
              type="password"
              autoComplete="current-password"
              value={password}
              onChange={(e) => setPassword(e.target.value)}
            />
            {message && <Banner tone="error">{message}</Banner>}
            <Button type="submit" variant="primary" disabled={busy || rateLimited || !username || !password}>
              Sign in
            </Button>
          </form>
        )}
      </Card>
    </div>
  );
}
