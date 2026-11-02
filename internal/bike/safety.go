package bike

// ClampLevel clamps req into [max(1,min), max(1,max)]. The result is never
// below 1 (level 0 reboots the bike) and never above the device maximum; if
// min > max, max wins.
func ClampLevel(req, min, max int) int {
	if max < 1 {
		max = 1
	}
	if min < 1 {
		min = 1
	}
	if min > max {
		min = max
	}
	if req < min {
		return min
	}
	if req > max {
		return max
	}
	return req
}
