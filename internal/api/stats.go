package api

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/metril/wattson/internal/auth"
	"github.com/metril/wattson/internal/stats"
	"github.com/metril/wattson/internal/store"
)

// TotalsPeriod is the sum of finished rides over one calendar period.
type TotalsPeriod struct {
	Start     string  `json:"start" doc:"First local day of the period, YYYY-MM-DD (the Monday for weeks)."`
	Rides     int     `json:"rides" doc:"Number of finished rides."`
	DurationS float64 `json:"duration_s" doc:"Total moving time in seconds."`
	DistanceM float64 `json:"distance_m" doc:"Total distance in metres."`
	AscentM   float64 `json:"ascent_m" doc:"Total climbing in metres."`
	KJ        float64 `json:"kj" doc:"Total work in kilojoules."`
	TSS       float64 `json:"tss" doc:"Total training stress score; rides without a TSS count as 0."`
}

// Streaks counts consecutive local days, and ISO weeks, with at least one ride.
type Streaks struct {
	CurrentDays  int `json:"current_days" doc:"Consecutive days with a ride, ending today or yesterday; 0 when the streak has lapsed."`
	LongestDays  int `json:"longest_days" doc:"Longest run of consecutive days with a ride."`
	CurrentWeeks int `json:"current_weeks" doc:"Consecutive ISO weeks with a ride, ending this week or last; 0 when lapsed."`
	LongestWeeks int `json:"longest_weeks" doc:"Longest run of consecutive ISO weeks with a ride."`
}

// TotalsOut is the totals-and-streaks response.
type TotalsOut struct {
	Period  string         `json:"period" doc:"The requested granularity: week, month or year."`
	TZ      string         `json:"tz" doc:"The IANA time zone used to assign rides to days."`
	Periods []TotalsPeriod `json:"periods" doc:"Oldest first and without gaps, ending with the current period: the last 26 weeks, 24 months or 10 years."`
	Streaks Streaks        `json:"streaks" doc:"Streaks over the whole ride history."`
}

// FitnessDay is one day of the fitness chart.
type FitnessDay struct {
	Date string  `json:"date" doc:"Local day, YYYY-MM-DD."`
	TSS  float64 `json:"tss" doc:"Training stress score of the day's rides; 0 on a rest day."`
	CTL  float64 `json:"ctl" doc:"Chronic training load (fitness), a 42-day exponentially weighted average of TSS."`
	ATL  float64 `json:"atl" doc:"Acute training load (fatigue), a 7-day exponentially weighted average of TSS."`
	TSB  float64 `json:"tsb" doc:"Training stress balance (form): ctl minus atl."`
}

// FitnessOut is the fitness-chart response.
type FitnessOut struct {
	TZ   string       `json:"tz" doc:"The IANA time zone used to assign rides to days."`
	Days []FitnessDay `json:"days" doc:"Oldest first, one entry per day up to today; empty when there are no rides."`
}

// CurvePoint is the best power for one duration.
type CurvePoint struct {
	DurS   int     `json:"dur_s" doc:"Duration in seconds."`
	Watts  float64 `json:"watts" doc:"Best average power over this duration, in watts."`
	WPerKg float64 `json:"w_per_kg" doc:"Watts per kilogram, using the rider's weight on that ride; 0 when unknown."`
	RideID int64   `json:"ride_id" doc:"The ride that set it."`
	Date   string  `json:"date" doc:"Start of that ride, UTC, YYYY-MM-DD."`
}

// PowerCurveOut is the all-time power curve response.
type PowerCurveOut struct {
	Window string       `json:"window" doc:"The requested window: 90d, 1y or all."`
	Points []CurvePoint `json:"points" doc:"About 45 points, shortest duration first; durations longer than any ride are absent."`
	EFTPW  *float64     `json:"eftp_w" nullable:"true" doc:"Estimated FTP: 95 percent of the best 20-minute power in the window; null without a 20-minute effort."`
}

type totalsIn struct {
	RiderID int64  `query:"rider_id" minimum:"0" doc:"Only this rider. A rider of another user is a 404 unless you are an admin. Default: every rider of yours."`
	Period  string `query:"period" enum:"week,month,year" default:"week" doc:"Bucket size. Weeks are ISO weeks starting Monday."`
	TZ      string `query:"tz" default:"UTC" doc:"IANA time zone (for example Europe/Paris) that decides which local day a ride belongs to."`
}

type fitnessIn struct {
	RiderID int64  `query:"rider_id" minimum:"0" doc:"Only this rider. A rider of another user is a 404 unless you are an admin. Default: every rider of yours."`
	Days    int    `query:"days" minimum:"7" maximum:"730" default:"90" doc:"How many days to return, 7 to 730."`
	TZ      string `query:"tz" default:"UTC" doc:"IANA time zone that decides which local day a ride belongs to."`
}

type curveIn struct {
	RiderID int64  `query:"rider_id" minimum:"0" doc:"Only this rider. A rider of another user is a 404 unless you are an admin. Default: every rider of yours."`
	Window  string `query:"window" enum:"90d,1y,all" default:"all" doc:"Only rides started within this window."`
}

func errBadTZ(tz string) error {
	return apiErr(http.StatusBadRequest, "invalid_request", "tz: unknown time zone "+tz)
}

// loadZone resolves an IANA zone name; empty means UTC.
func loadZone(tz string) (*time.Location, error) {
	if tz == "" {
		return time.UTC, nil
	}
	loc, err := time.LoadLocation(tz)
	if err != nil || tz == "Local" {
		return nil, errBadTZ(tz)
	}
	return loc, nil
}

// statsRider resolves the optional rider_id to the (userID, riderID) the
// store queries take, checking ownership like the rides list does.
func (d Deps) statsRider(ctx context.Context, riderID int64) (userID, rid int64, err error) {
	ident, _ := auth.FromContext(ctx)
	if riderID != 0 {
		rd, err := d.Store.RiderByID(ctx, riderID)
		if errors.Is(err, store.ErrNotFound) {
			return 0, 0, errNoRider()
		}
		if err != nil {
			return 0, 0, err
		}
		if rd.UserID != ident.User.ID && !ident.Has(auth.ScopeAdmin) {
			return 0, 0, errNoRider()
		}
		return rd.UserID, rd.ID, nil
	}
	if ident.User.ID == 0 {
		return 0, 0, errors.New("stats: identity has no user")
	}
	return ident.User.ID, 0, nil
}

// dayRides are the sorted distinct local days with a ride and the TSS per day.
func dayRides(bs []stats.Bucket, loc *time.Location) (days []int, tss map[int]float64) {
	tss = make(map[int]float64, len(bs))
	for _, b := range bs { // bs is oldest first, so days stay sorted
		d := stats.DayOrd(b.At, loc)
		if len(days) == 0 || days[len(days)-1] != d {
			days = append(days, d)
		}
		tss[d] += b.TSS
	}
	return days, tss
}

func (d Deps) registerStats(api huma.API) {
	register(api, route{Method: http.MethodGet, Path: "/api/stats/totals", ID: "getStatsTotals", Tag: "stats",
		Summary: "Ride totals and streaks",
		Description: "Returns ride count, time, distance, climbing, work and TSS per week, month or year, oldest first and " +
			"zero-filled up to the current period, plus the current and longest streaks of consecutive days and ISO weeks " +
			"with a ride. Rides are assigned to days in the time zone given by `tz`. With `rider_id` only that rider is " +
			"counted; a rider of another user is a 404 unless the caller is an admin.",
		Scope: auth.ScopeRead, Errors: []int{http.StatusNotFound}},
		func(ctx context.Context, in *totalsIn) (*struct{ Body TotalsOut }, error) {
			loc, err := loadZone(in.TZ)
			if err != nil {
				return nil, err
			}
			uid, rid, err := d.statsRider(ctx, in.RiderID)
			if err != nil {
				return nil, err
			}
			bs, err := d.Store.RideBuckets(ctx, uid, rid)
			if err != nil {
				return nil, err
			}
			now := d.Now()
			days, _ := dayRides(bs, loc)
			sk := stats.Streaks(days, stats.DayOrd(now, loc))
			out := TotalsOut{Period: in.Period, TZ: loc.String(), Streaks: Streaks(sk)}
			ts := stats.Totals(bs, in.Period, loc, now)
			out.Periods = make([]TotalsPeriod, len(ts))
			for i, t := range ts {
				out.Periods[i] = TotalsPeriod(t)
			}
			return &struct{ Body TotalsOut }{out}, nil
		})

	register(api, route{Method: http.MethodGet, Path: "/api/stats/fitness", ID: "getStatsFitness", Tag: "stats",
		Summary: "Fitness, fatigue and form",
		Description: "Returns daily chronic training load (CTL, 42-day constant), acute training load (ATL, 7-day constant) " +
			"and training stress balance (TSB = CTL - ATL) from ride TSS. Days without a ride count as zero load. The " +
			"series is computed from the first ride, so the returned window is warmed up. Rides are assigned to days in the " +
			"time zone given by `tz`.",
		Scope: auth.ScopeRead, Errors: []int{http.StatusNotFound}},
		func(ctx context.Context, in *fitnessIn) (*struct{ Body FitnessOut }, error) {
			loc, err := loadZone(in.TZ)
			if err != nil {
				return nil, err
			}
			uid, rid, err := d.statsRider(ctx, in.RiderID)
			if err != nil {
				return nil, err
			}
			bs, err := d.Store.RideBuckets(ctx, uid, rid)
			if err != nil {
				return nil, err
			}
			out := FitnessOut{TZ: loc.String(), Days: []FitnessDay{}}
			days, tss := dayRides(bs, loc)
			if len(days) > 0 {
				today := stats.DayOrd(d.Now(), loc)
				pts := stats.PMC(tss, days[0], max(today, days[len(days)-1]))
				if len(pts) > in.Days {
					pts = pts[len(pts)-in.Days:]
				}
				out.Days = make([]FitnessDay, len(pts))
				for i, p := range pts {
					out.Days[i] = FitnessDay{Date: stats.DayString(p.Day), TSS: p.TSS, CTL: p.CTL, ATL: p.ATL, TSB: p.TSB}
				}
			}
			return &struct{ Body FitnessOut }{out}, nil
		})

	register(api, route{Method: http.MethodGet, Path: "/api/stats/power-curve", ID: "getStatsPowerCurve", Tag: "stats",
		Summary: "All-time power curve",
		Description: "Returns the best average power for each of about 45 durations from 1 second to 2 hours across the " +
			"caller's finished rides in the window, with watts per kilogram and the ride that set each point, plus the " +
			"estimated FTP (95 percent of the best 20-minute power). With `rider_id` only that rider's rides are used; a " +
			"rider of another user is a 404 unless the caller is an admin.",
		Scope: auth.ScopeRead, Errors: []int{http.StatusNotFound}},
		func(ctx context.Context, in *curveIn) (*struct{ Body PowerCurveOut }, error) {
			uid, rid, err := d.statsRider(ctx, in.RiderID)
			if err != nil {
				return nil, err
			}
			var since time.Time
			switch in.Window {
			case "90d":
				since = d.Now().AddDate(0, 0, -90)
			case "1y":
				since = d.Now().AddDate(-1, 0, 0)
			}
			best, err := d.Store.BestPowerCurve(ctx, uid, rid, since)
			if err != nil {
				return nil, err
			}
			out := PowerCurveOut{Window: in.Window, Points: make([]CurvePoint, len(best))}
			pts := make([]stats.Point, len(best))
			for i, b := range best {
				cp := CurvePoint{DurS: b.DurS, Watts: b.Watts, RideID: b.RideID, Date: b.StartedAt.UTC().Format(time.DateOnly)}
				if b.WeightKg > 0 {
					cp.WPerKg = b.Watts / b.WeightKg
				}
				out.Points[i] = cp
				pts[i] = stats.Point{DurS: b.DurS, Watts: b.Watts}
			}
			if v, ok := stats.EFTP(pts); ok {
				out.EFTPW = &v
			}
			return &struct{ Body PowerCurveOut }{out}, nil
		})
}
