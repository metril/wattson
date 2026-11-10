package stats

import "time"

// Attempt is one finished ride on a route.
type Attempt struct {
	RideID                                int64
	StartedAt                             time.Time
	ElapsedS, AvgSpeedKmh, NPW, AvgPowerW float64
	AvgHRBpm                              int
	Completed                             bool
}

// RouteSummary aggregates a rider's attempts on one route. Time fields are
// nil when there is no completed attempt; HR ignores rides without a reading.
type RouteSummary struct {
	Attempts, Completed          int
	BestS, AvgS, LastS           *float64
	BestSpeedKmh                 *float64
	PRRideID                     int64
	PRDate                       *time.Time
	BestNPW, AvgPowerW, AvgHRBpm *float64
	// IsPR is parallel to the input: true only for the current PR attempt.
	IsPR []bool
}

// SummarizeRoute summarises attempts (oldest first). The PR is the completed
// attempt with the lowest elapsed time; the earliest wins a tie.
func SummarizeRoute(as []Attempt) RouteSummary {
	s := RouteSummary{Attempts: len(as), IsPR: make([]bool, len(as))}
	pr := -1
	var sumT, sumP, sumHR float64
	var nHR int
	for i, a := range as {
		if !a.Completed || a.ElapsedS <= 0 {
			continue
		}
		s.Completed++
		sumT += a.ElapsedS
		sumP += a.AvgPowerW
		if a.AvgHRBpm > 0 {
			sumHR += float64(a.AvgHRBpm)
			nHR++
		}
		if pr < 0 || a.ElapsedS < as[pr].ElapsedS {
			pr = i
		}
		last := a.ElapsedS
		s.LastS = &last
		if s.BestSpeedKmh == nil || a.AvgSpeedKmh > *s.BestSpeedKmh {
			v := a.AvgSpeedKmh
			s.BestSpeedKmh = &v
		}
		if s.BestNPW == nil || a.NPW > *s.BestNPW {
			v := a.NPW
			s.BestNPW = &v
		}
	}
	if pr < 0 {
		return s
	}
	best, avg, avgP := as[pr].ElapsedS, sumT/float64(s.Completed), sumP/float64(s.Completed)
	d := as[pr].StartedAt
	s.BestS, s.AvgS, s.AvgPowerW, s.PRRideID, s.PRDate, s.IsPR[pr] = &best, &avg, &avgP, as[pr].RideID, &d, true
	if nHR > 0 {
		v := sumHR / float64(nHR)
		s.AvgHRBpm = &v
	}
	return s
}
