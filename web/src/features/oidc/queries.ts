import { useQuery } from '@tanstack/react-query';
import { api, unwrap } from '@/lib/api';
import type { components } from '@/lib/openapi';

export type ProviderOut = components['schemas']['ProviderOut'];
export const PROVIDERS_KEY = ['oidc-providers'] as const;

export function useProviders() {
  return useQuery<ProviderOut[]>({
    queryKey: PROVIDERS_KEY,
    queryFn: async () => unwrap(await api.GET('/api/admin/oidc-providers')),
  });
}
