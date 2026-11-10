package stats

// GhostStepM is the minimum spacing of ghost points in metres.
const GhostStepM = 10

// GhostTrack thins a ride's (distance, elapsed) samples to one point at least
// GhostStepM metres after the previous kept one, always keeping the first and
// last. Samples must be in ride order.
func GhostTrack(dist, elapsed []float64) [][2]float64 {
	n := min(len(dist), len(elapsed))
	if n == 0 {
		return nil
	}
	out := make([][2]float64, 0, n/4+2)
	out = append(out, [2]float64{dist[0], elapsed[0]})
	for i := 1; i < n; i++ {
		if dist[i]-out[len(out)-1][0] >= GhostStepM {
			out = append(out, [2]float64{dist[i], elapsed[i]})
		}
	}
	if last := [2]float64{dist[n-1], elapsed[n-1]}; last[0] > out[len(out)-1][0] {
		out = append(out, last)
	}
	return out
}
