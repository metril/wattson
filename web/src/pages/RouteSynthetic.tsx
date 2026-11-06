import { useUnits } from '@/lib/auth';
import { distUnit, elevUnit, toDist, toElev } from '@/lib/format';
import { useMemo, useState } from 'react';
import { Link, useNavigate } from 'react-router';
import { useMutation, useQueryClient } from '@tanstack/react-query';
import { Plus, Trash2 } from 'lucide-react';
import { api, unwrap } from '@/lib/api';
import { errMessage } from '@/lib/errors';
import { Banner } from '@/components/Banner';
import { Button } from '@/components/Button';
import { Card } from '@/components/Card';
import { Field } from '@/components/Field';
import { ROUTES_KEY } from '@/features/routes/queries';
import { ProfileCanvas } from '@/features/routes/ProfileCanvas';
import { syntheticProfile } from '@/lib/route-math';

type Row = { length: string; grade: string };

const MAX_TOTAL_M = 500_000;

const valid = (r: Row) => {
  const l = Number(r.length);
  const g = Number(r.grade);
  return r.length.trim() !== '' && r.grade.trim() !== '' && l >= 10 && l <= 100000 && g >= -25 && g <= 25;
};

export function RouteSyntheticPage() {
  const units = useUnits();
  const navigate = useNavigate();
  const qc = useQueryClient();
  const [name, setName] = useState('');
  const [rows, setRows] = useState<Row[]>([{ length: '1000', grade: '0' }]);
  const [error, setError] = useState<string | null>(null);

  const segs = useMemo(
    () => rows.filter(valid).map((r) => ({ length_m: Number(r.length), grade_pct: Number(r.grade) })),
    [rows],
  );
  const synthetic = useMemo(() => syntheticProfile(segs), [segs]);
  const totalM = segs.reduce((a, s) => a + s.length_m, 0);
  const ascent = segs.reduce((a, s) => a + (s.grade_pct > 0 ? (s.length_m * s.grade_pct) / 100 : 0), 0);
  const tooLong = totalM > MAX_TOTAL_M;
  const allValid = rows.length > 0 && rows.every(valid) && !tooLong;

  const save = useMutation({
    mutationFn: async () =>
      unwrap(await api.POST('/api/routes/synthetic', { body: { name: name.trim(), segments: segs } })),
    onSuccess: (r) => {
      void qc.invalidateQueries({ queryKey: ROUTES_KEY });
      navigate(`/routes/${r.id}`);
    },
    onError: (e) => setError(errMessage(e)),
  });

  const set = (i: number, k: keyof Row, v: string) =>
    setRows((rs) => rs.map((r, j) => (j === i ? { ...r, [k]: v } : r)));

  return (
    <div className="mx-auto flex w-full max-w-6xl flex-col gap-4">
      <div className="flex flex-wrap items-center gap-3">
        <h1 className="numeral flex-1 text-2xl">Synthetic route</h1>
        <Link to="/routes" className="inline-flex min-h-touch items-center px-2 text-dim hover:text-chalk">
          Back to routes
        </Link>
      </div>

      <div className="grid gap-4 lg:grid-cols-[minmax(0,22rem)_minmax(0,1fr)]">
        <Card className="flex flex-col gap-4">
          <Field label="Name" value={name} maxLength={100} onChange={(e) => setName(e.target.value)} />
          <ol className="flex flex-col gap-3">
            {rows.map((r, i) => (
              <li key={i} className="flex items-end gap-2">
                <Field
                  label={`Segment ${i + 1} length (m)`}
                  type="number"
                  inputMode="decimal"
                  min={10}
                  max={100000}
                  value={r.length}
                  onChange={(e) => set(i, 'length', e.target.value)}
                />
                <Field
                  label={`Segment ${i + 1} grade (%)`}
                  type="number"
                  inputMode="decimal"
                  min={-25}
                  max={25}
                  step={0.5}
                  value={r.grade}
                  onChange={(e) => set(i, 'grade', e.target.value)}
                />
                <Button
                  aria-label={`Remove segment ${i + 1}`}
                  disabled={rows.length === 1}
                  onClick={() => setRows((rs) => rs.filter((_, j) => j !== i))}
                >
                  <Trash2 aria-hidden className="size-5" />
                </Button>
              </li>
            ))}
          </ol>
          <Button
            onClick={() => setRows((rs) => [...rs, { length: '500', grade: rs.at(-1)?.grade ?? '0' }])}
            disabled={rows.length >= 100}
          >
            <Plus aria-hidden className="size-5" />
            Add segment
          </Button>
        </Card>

        <Card className="flex flex-col gap-4">
          <ProfileCanvas units={units} profile={null} synthetic={synthetic} height={220} />
          <p className="numeral text-2xl" aria-live="polite">
            {toDist(totalM, units).toFixed(2)} {distUnit(units)} · +{Math.round(toElev(ascent, units))} {elevUnit(units)}
          </p>
          <p className="text-sm text-dim">
            Segment lengths run 10 m to 100 km, grades -25 to 25 %. The route starts at 100 m elevation.
          </p>
          {tooLong && <Banner tone="warn">A route can be at most 500 km in total. Shorten a segment or remove one.</Banner>}
          {error && <Banner tone="error">{error}</Banner>}
          <div>
            <Button
              variant="primary"
              disabled={save.isPending || !name.trim() || !allValid}
              onClick={() => {
                setError(null);
                save.mutate();
              }}
            >
              Save route
            </Button>
          </div>
        </Card>
      </div>
    </div>
  );
}
