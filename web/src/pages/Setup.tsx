import { useEffect, useRef, useState } from 'react';
import { useNavigate } from 'react-router';
import { useQueryClient } from '@tanstack/react-query';
import { api, ApiError, unwrap } from '@/lib/api';
import { AUTH_INFO_KEY, ME_KEY } from '@/lib/auth';
import { errMessage, parseSettingError } from '@/lib/errors';
import { Banner } from '@/components/Banner';
import { Button } from '@/components/Button';
import { Card } from '@/components/Card';
import { Field } from '@/components/Field';
import { BikeForm } from '@/pages/settings/Bike';
import { ProviderForm } from '@/features/oidc/ProviderForm';

const STEPS = ['Admin account', 'Public URL', 'Bike connection', 'Sign-in provider', 'All set'] as const;
const MIN_PASSWORD = 10;

function Stepper({ step }: { step: number }) {
  return (
    <ol aria-label="Setup progress" className="flex gap-1.5">
      {STEPS.map((s, i) => (
        <li
          key={s}
          aria-current={i === step ? 'step' : undefined}
          className={`h-1.5 flex-1 rounded-full ${i <= step ? 'bg-accent' : 'bg-raised'}`}
        >
          <span className="sr-only">{`Step ${i + 1}: ${s}`}</span>
        </li>
      ))}
    </ol>
  );
}

export function SetupPage() {
  const navigate = useNavigate();
  const qc = useQueryClient();
  const [step, setStep] = useState(0);
  const [username, setUsername] = useState('admin');
  const [password, setPassword] = useState('');
  const [confirm, setConfirm] = useState('');
  const [publicUrl, setPublicUrl] = useState(window.location.origin);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [urlError, setUrlError] = useState<string | null>(null);

  const created = useRef(false);
  // Setup is one-shot: once an admin exists this screen has nothing to do.
  useEffect(() => {
    let live = true;
    api
      .GET('/api/setup/status')
      .then((r) => {
        if (live && !created.current && r.data && !r.data.needs_setup) navigate('/login', { replace: true });
      })
      .catch(() => {});
    return () => {
      live = false;
    };
  }, [navigate]);

  const next = () => setStep((s) => s + 1);
  const mismatch = confirm !== '' && password !== confirm;
  const tooShort = password !== '' && password.length < MIN_PASSWORD;
  const adminOk = username.trim().length >= 3 && password.length >= MIN_PASSWORD && password === confirm;

  async function createAdmin() {
    setBusy(true);
    setError(null);
    try {
      unwrap(await api.POST('/api/setup', { body: { username: username.trim(), password } }));
      created.current = true;
      // The new admin is signed in; drop cached "anonymous / needs setup" answers.
      qc.removeQueries({ queryKey: ME_KEY });
      qc.removeQueries({ queryKey: AUTH_INFO_KEY });
      next();
    } catch (e) {
      if (e instanceof ApiError && e.code === 'already_setup') navigate('/login', { replace: true });
      else setError(errMessage(e));
    } finally {
      setBusy(false);
    }
  }

  async function savePublicUrl() {
    setBusy(true);
    setUrlError(null);
    try {
      unwrap(await api.PATCH('/api/settings', { body: { general: { public_url: publicUrl.trim() } } }));
      next();
    } catch (e) {
      setUrlError(parseSettingError(e).text);
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="mx-auto flex min-h-dvh max-w-lg flex-col justify-center gap-4 p-4">
      <h1 className="numeral text-2xl">Set up Wattson</h1>
      <Stepper step={step} />
      <Card className="flex flex-col gap-4">
        <h2 className="numeral text-xl">{STEPS[step]}</h2>

        {step === 0 && (
          <form
            className="flex flex-col gap-4"
            onSubmit={(e) => {
              e.preventDefault();
              if (adminOk) void createAdmin();
            }}
          >
            <p className="text-sm text-dim">Create the first administrator. You can add more people later.</p>
            <Field
              label="Username"
              autoComplete="username"
              autoCapitalize="none"
              value={username}
              onChange={(e) => setUsername(e.target.value)}
              hint="3 to 64 letters, digits, dot, underscore or hyphen."
            />
            <Field
              label="Password"
              type="password"
              autoComplete="new-password"
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              error={tooShort ? `Use at least ${MIN_PASSWORD} characters.` : undefined}
              hint={tooShort ? undefined : `At least ${MIN_PASSWORD} characters.`}
            />
            <Field
              label="Confirm password"
              type="password"
              autoComplete="new-password"
              value={confirm}
              onChange={(e) => setConfirm(e.target.value)}
              error={mismatch ? "Passwords don't match." : undefined}
            />
            {error && <Banner tone="error">{error}</Banner>}
            <Button type="submit" variant="primary" disabled={!adminOk || busy}>
              Next
            </Button>
          </form>
        )}

        {step === 1 && (
          <form
            className="flex flex-col gap-4"
            onSubmit={(e) => {
              e.preventDefault();
              void savePublicUrl();
            }}
          >
            <Field
              label="Public URL"
              type="url"
              inputMode="url"
              value={publicUrl}
              onChange={(e) => setPublicUrl(e.target.value)}
              error={urlError ?? undefined}
              hint="Where browsers reach Wattson, with no path. Single sign-in redirects are built from it."
            />
            <div className="flex flex-wrap gap-3">
              <Button type="submit" variant="primary" disabled={busy || !publicUrl.trim()}>
                Next
              </Button>
              <Button onClick={next}>Skip</Button>
            </div>
          </form>
        )}

        {step === 2 && (
          <>
            <p className="text-sm text-dim">Skip to keep the built-in simulator; no bike needed.</p>
            <BikeForm compact onSaved={next} />
            <Button onClick={next}>Skip</Button>
          </>
        )}

        {step === 3 && (
          <>
            <p className="text-sm text-dim">Let people sign in with your identity provider. You can add one later.</p>
            <ProviderForm compact onDone={next} />
            <Button onClick={next}>Skip</Button>
          </>
        )}

        {step === 4 && (
          <>
            <p className="text-dim">Wattson is ready. Settings can be changed any time from the Settings page.</p>
            <div>
              <Button variant="primary" onClick={() => navigate('/', { replace: true })}>
                Open Wattson
              </Button>
            </div>
          </>
        )}
      </Card>
    </div>
  );
}
