// Package planeval evaluates the route planner across many synthetic rosters of
// different shapes, so a planner change has to hold up everywhere, not on one
// roster. Rosters are deterministic per scenario and seed; nothing here talks
// to Google.
package planeval

import (
	"fmt"
	"hash/fnv"
	"math"
	"math/rand/v2"
	"ride-home-router/internal/models"
	"slices"
)

// Region is where a person lives, at the granularity the operator plans in.
type Region string

const (
	Durham     Region = "Durham"
	ChapelHill Region = "Chapel Hill"
	Carrboro   Region = "Carrboro"
	Raleigh    Region = "Raleigh/Cary"
)

// Mix is the share of people in each region, in any units; it is normalised.
type Mix map[Region]float64

type centroid struct {
	Name   string
	Region Region
	Lat    float64
	Lng    float64
	Street string
}

// Approximate residential neighbourhood centres, spread with a 1 km Gaussian.
var centroids = []centroid{
	{"Trinity Park", Durham, 36.011, -78.911, "Monmouth Ave"},
	{"Hope Valley", Durham, 35.946, -78.957, "Dover Rd"},
	{"Southpoint/Woodcroft", Durham, 35.923, -78.938, "Woodcroft Pkwy"},
	{"Duke Park", Durham, 36.018, -78.891, "Acadia St"},
	{"Northgate Park", Durham, 36.029, -78.900, "Lavender Ave"},
	{"Southern Village", ChapelHill, 35.880, -79.066, "Market St"},
	{"Meadowmont", ChapelHill, 35.904, -79.009, "Meadowmont Ln"},
	{"Governors Club", ChapelHill, 35.846, -79.034, "Governors Dr"},
	{"Downtown Carrboro", Carrboro, 35.912, -79.078, "N Greensboro St"},
	{"North Carrboro", Carrboro, 35.931, -79.089, "Hillsborough Rd"},
	{"North Hills", Raleigh, 35.839, -78.646, "Northbrook Dr"},
	{"Five Points", Raleigh, 35.809, -78.646, "Whitaker Mill Rd"},
	{"Brier Creek", Raleigh, 35.912, -78.791, "Brier Creek Pkwy"},
	{"Cary Parkway", Raleigh, 35.766, -78.823, "SW Cary Pkwy"},
	{"Morrisville", Raleigh, 35.828, -78.837, "Morrisville Carpenter Rd"},
}

// DurhamVenue and ChapelHillVenue are the two activity locations scenarios use.
var (
	DurhamVenue     = models.Coordinates{Lat: 35.996, Lng: -78.899}
	ChapelHillVenue = models.Coordinates{Lat: 35.913, Lng: -79.056}
)

// RegionOf returns the region of the nearest neighbourhood centre.
func RegionOf(c models.Coordinates) Region {
	best, bestKm := centroids[0], math.Inf(1)
	for _, n := range centroids {
		if d := haversineKm(c, models.Coordinates{Lat: n.Lat, Lng: n.Lng}); d < bestKm {
			best, bestKm = n, d
		}
	}
	return best.Region
}

// Scenario describes one roster shape. Drivers are generated until their seats
// reach Riders×SeatFactor, so no scenario is generous with seats. Households
// are pairs at one address, which is how the planner recognises them.
type Scenario struct {
	Name       string
	Riders     int
	RiderMix   Mix
	DriverMix  Mix     // nil: drivers live where riders live
	Vans       float64 // share of drivers with 7 to 9 seats
	Households float64 // share of riders who share a home with one other rider (pairs)
	SeatFactor float64 // seats per rider, e.g. 1.08; 0 means 1.08
	Venue      models.Coordinates
}

// Roster is one generated population ready for the planner.
type Roster struct {
	Participants []models.Participant
	Drivers      []models.Driver
	Venue        models.Coordinates
}

var (
	firstNames = []string{"Amelia", "Noah", "Olivia", "Liam", "Emma", "Elijah", "Charlotte", "Mateo", "Sophia", "Lucas", "Isabella", "James", "Mia", "Henry", "Evelyn", "Theo", "Harper", "Daniel", "Sofia", "Michael", "Avery", "Ethan", "Ella", "Samuel", "Aria", "Jackson", "Scarlett", "Leo", "Grace", "Owen", "Chloe", "Aiden", "Nora", "Gabriel", "Riley", "Julian", "Hazel", "Isaac", "Lily", "Ezra"}
	lastNames  = []string{"Bennett", "Carter", "Davis", "Evans", "Flores", "Garcia", "Harris", "Irving", "Johnson", "Kim", "Lewis", "Martin", "Nguyen", "Ortiz", "Patel", "Quinn", "Rivera", "Singh", "Turner", "Walker", "Young", "Brooks", "Campbell", "Diaz", "Foster", "Gray", "Hughes", "Kelly", "Lopez", "Morgan"}
)

// Generate builds the roster for a scenario and seed. The same inputs always
// produce the same roster.
func Generate(s Scenario, seed uint64) Roster {
	h := fnv.New64a()
	_, _ = h.Write([]byte(s.Name))
	rng := rand.New(rand.NewPCG(seed, h.Sum64())) //nolint:gosec // Deterministic synthetic data, not security material.
	seatFactor := s.SeatFactor
	if seatFactor == 0 {
		seatFactor = 1.08
	}
	venue := s.Venue
	if venue == (models.Coordinates{}) {
		venue = DurhamVenue
	}
	// A pair is created with probability p per home; the share of riders in
	// pairs is then 2p/(1+p), so p = h/(2-h) gives the requested share h.
	pairChance := s.Households / (2 - s.Households)
	riderPick := picker(s.RiderMix)
	driverPick := riderPick
	if s.DriverMix != nil {
		driverPick = picker(s.DriverMix)
	}
	roster := Roster{Venue: venue}
	for i := 0; len(roster.Participants) < s.Riders; i++ {
		c := riderPick(rng)
		lat, lng := jitter(rng, c)
		address := fmt.Sprintf("%d %s, %s, NC", 100+i*3, c.Street, c.Region)
		roster.Participants = append(roster.Participants, models.Participant{ID: int64(len(roster.Participants) + 1), Name: name(rng), Address: address, Lat: lat, Lng: lng})
		if rng.Float64() < pairChance && len(roster.Participants) < s.Riders {
			roster.Participants = append(roster.Participants, models.Participant{ID: int64(len(roster.Participants) + 1), Name: name(rng), Address: address, Lat: lat, Lng: lng})
		}
	}
	seats := 0
	for i := 0; float64(seats) < float64(s.Riders)*seatFactor; i++ {
		c := driverPick(rng)
		lat, lng := jitter(rng, c)
		capacity := 4
		switch r := rng.Float64(); {
		case r < s.Vans:
			capacity = 7 + rng.IntN(3)
		case r < s.Vans+0.15:
			capacity = 3
		}
		roster.Drivers = append(roster.Drivers, models.Driver{ID: int64(i + 1), Name: name(rng), Address: fmt.Sprintf("%d %s, %s, NC", 2000+i*5, c.Street, c.Region), Lat: lat, Lng: lng, VehicleCapacity: capacity})
		seats += capacity
	}
	return roster
}

func picker(mix Mix) func(*rand.Rand) centroid {
	regions := make([]Region, 0, len(mix))
	for region := range mix {
		regions = append(regions, region)
	}
	slices.Sort(regions)
	total := 0.0
	for _, r := range regions {
		total += mix[r]
	}
	return func(rng *rand.Rand) centroid {
		x := rng.Float64() * total
		region := regions[len(regions)-1]
		for _, r := range regions {
			if x < mix[r] {
				region = r
				break
			}
			x -= mix[r]
		}
		var candidates []centroid
		for _, c := range centroids {
			if c.Region == region {
				candidates = append(candidates, c)
			}
		}
		return candidates[rng.IntN(len(candidates))]
	}
}

// jitter spreads a point around a centre with a 1 km Gaussian, capped at 2 km.
func jitter(rng *rand.Rand, c centroid) (float64, float64) {
	for {
		x, y := rng.NormFloat64(), rng.NormFloat64()
		if x*x+y*y <= 4 {
			return c.Lat + y/111.195, c.Lng + x/(111.195*math.Cos(c.Lat*math.Pi/180))
		}
	}
}

func name(rng *rand.Rand) string {
	return firstNames[rng.IntN(len(firstNames))] + " " + lastNames[rng.IntN(len(lastNames))]
}

func haversineKm(a, b models.Coordinates) float64 {
	const r = 6371.0088
	la1, lo1, la2, lo2 := a.Lat*math.Pi/180, a.Lng*math.Pi/180, b.Lat*math.Pi/180, b.Lng*math.Pi/180
	h := math.Pow(math.Sin((la2-la1)/2), 2) + math.Cos(la1)*math.Cos(la2)*math.Pow(math.Sin((lo2-lo1)/2), 2)
	return 2 * r * math.Asin(math.Sqrt(h))
}

// Scenarios is the suite: the roster shapes the operator actually sees, at
// several sizes, plus the shapes that exposed problems in field tests.
func Scenarios() []Scenario {
	chCarrboro := Mix{ChapelHill: 45, Carrboro: 40, Durham: 15}
	carrboroDurham := Mix{Carrboro: 45, Durham: 55}
	durhamHeavy := Mix{Durham: 80, ChapelHill: 10, Carrboro: 10}
	triangle := Mix{Durham: 45, ChapelHill: 15, Carrboro: 10, Raleigh: 30}
	list := []Scenario{}
	for _, n := range []int{50, 100, 300, 500} {
		list = append(list, Scenario{Name: fmt.Sprintf("chapel-hill-carrboro-%d", n), Riders: n, RiderMix: chCarrboro, Households: 0.1})
	}
	list = append(list, Scenario{Name: "chapel-hill-carrboro-vans-300", Riders: 300, RiderMix: chCarrboro, Vans: 0.3, Households: 0.1})
	for _, n := range []int{50, 100, 300, 500} {
		list = append(list, Scenario{Name: fmt.Sprintf("carrboro-durham-%d", n), Riders: n, RiderMix: carrboroDurham, Households: 0.1})
	}
	for _, n := range []int{100, 300} {
		list = append(list, Scenario{Name: fmt.Sprintf("durham-heavy-%d", n), Riders: n, RiderMix: durhamHeavy, Households: 0.1})
	}
	for _, n := range []int{100, 300, 500} {
		list = append(list, Scenario{Name: fmt.Sprintf("triangle-wide-%d", n), Riders: n, RiderMix: triangle, Households: 0.1})
	}
	list = append(list,
		Scenario{Name: "raleigh-heavy-100", Riders: 100, RiderMix: Mix{Raleigh: 75, Durham: 25}, Households: 0.1},
		// Riders in Chapel Hill and Carrboro, drivers mostly in Durham: the field-test Carrboro problem.
		Scenario{Name: "drivers-in-durham-100", Riders: 100, RiderMix: Mix{ChapelHill: 40, Carrboro: 40, Durham: 20}, DriverMix: Mix{Durham: 80, ChapelHill: 10, Carrboro: 10}, Households: 0.1},
		Scenario{Name: "drivers-in-durham-300", Riders: 300, RiderMix: Mix{ChapelHill: 40, Carrboro: 40, Durham: 20}, DriverMix: Mix{Durham: 80, ChapelHill: 10, Carrboro: 10}, Households: 0.1},
		Scenario{Name: "vans-300", Riders: 300, RiderMix: triangle, Vans: 0.3, Households: 0.1},
		Scenario{Name: "vans-500", Riders: 500, RiderMix: triangle, Vans: 0.3, Households: 0.1},
		// Seats barely cover riders and many households: the bearing-sweep seed
		// fails on some seeds and the round-robin fallback is slow and poor. This
		// is recorded in the baseline on purpose; a seed-phase fix must show here.
		Scenario{Name: "tight-seats-500", Riders: 500, RiderMix: triangle, SeatFactor: 1.02, Households: 0.18},
		Scenario{Name: "chapel-hill-venue-100", Riders: 100, RiderMix: Mix{ChapelHill: 50, Carrboro: 30, Durham: 20}, Households: 0.1, Venue: ChapelHillVenue},
	)
	return list
}
