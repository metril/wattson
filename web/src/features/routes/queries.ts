export const ROUTES_KEY = ['routes'] as const;
export const routeKey = (id: number) => ['routes', id] as const;
export const profileKey = (id: number) => ['routes', id, 'profile'] as const;
