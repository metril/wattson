import { useQuery } from '@tanstack/react-query';
import { Select } from '@/components/Select';
import { api, unwrap } from '@/lib/api';
import { ROUTES_KEY } from '@/features/routes/queries';

/** "Free ride" or one of the saved routes; the choice is used by the next Start. */
export function RoutePicker({ value, onChange }: { value: number | null; onChange(id: number | null): void }) {
  const q = useQuery({
    queryKey: ROUTES_KEY,
    staleTime: Infinity,
    queryFn: async () => unwrap(await api.GET('/api/routes')),
  });
  const list = q.data ?? [];
  return (
    <Select
      label="Route"
      value={value === null || (q.data && !list.some((r) => r.id === value)) ? '' : String(value)}
      onChange={(e) => onChange(e.target.value === '' ? null : Number(e.target.value))}
    >
      <option value="">Free ride</option>
      {list.map((r) => (
        <option key={r.id} value={r.id}>
          {r.name}
        </option>
      ))}
    </Select>
  );
}
