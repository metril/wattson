/** Not under RIDES_KEY: ride-list cache updaters assume every key there holds ride pages. */
export const STATS_KEY = ['stats'] as const;

/** Key of the best-power-curve query; shared by the Stats page and the ride page. */
export const curveKey = (rider: number, window: string) => [...STATS_KEY, 'curve', rider, window] as const;
