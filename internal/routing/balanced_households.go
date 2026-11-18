package routing

import (
	"context"
	"fmt"
	"ride-home-router/internal/models"
	"slices"
	"sort"
	"strconv"
	"strings"
	"unicode"
)

type participantGroup struct {
	key     string
	bearing float64
	members []*models.Participant
	address string
	lat     float64
	lng     float64
}

func groupParticipantsByAddress(participants []*models.Participant) []*participantGroup {
	householdMap := make(map[string]*participantGroup)

	for _, p := range participants {
		key := householdKey(p)

		if group, exists := householdMap[key]; exists {
			group.members = append(group.members, p)
		} else {
			householdMap[key] = &participantGroup{key: key, members: []*models.Participant{p}, address: p.Address, lat: models.RoundCoordinate(p.Lat), lng: models.RoundCoordinate(p.Lng)}
		}
	}

	groups := make([]*participantGroup, 0, len(householdMap))
	for _, group := range householdMap {
		groups = append(groups, group)
	}

	sort.Slice(groups, func(i, j int) bool {
		if len(groups[i].members) != len(groups[j].members) {
			return len(groups[i].members) > len(groups[j].members)
		}
		return participantGroupKey(groups[i]) < participantGroupKey(groups[j])
	})

	return groups
}

func coordinateKey(lat, lng float64) string {
	return fmt.Sprintf("%.5f,%.5f", lat, lng)
}

func normalizeAddress(address string) string {
	address = strings.Map(func(r rune) rune {
		if unicode.IsPunct(r) {
			if r == '#' {
				return r
			}
			return -1
		}
		return unicode.ToLower(r)
	}, address)
	words := strings.Fields(strings.ReplaceAll(address, "#", " unit "))
	for i, word := range words {
		switch word {
		case "st":
			words[i] = "street"
		case "ave":
			words[i] = "avenue"
		case "rd":
			words[i] = "road"
		case "dr":
			words[i] = "drive"
		case "blvd":
			words[i] = "boulevard"
		case "ln":
			words[i] = "lane"
		case "ct":
			words[i] = "court"
		case "pl":
			words[i] = "place"
		case "n":
			words[i] = "north"
		case "s":
			words[i] = "south"
		case "e":
			words[i] = "east"
		case "w":
			words[i] = "west"
		case "apt", "apartment":
			words[i] = "unit"
		}
	}
	normalized := words[:0]
	for _, word := range words {
		if word == "unit" && len(normalized) > 0 && normalized[len(normalized)-1] == "unit" {
			continue
		}
		normalized = append(normalized, word)
	}
	return strings.Join(normalized, " ")
}

func HouseholdKey(participant *models.Participant) string { return householdKey(participant) }

func householdKey(participant *models.Participant) string {
	if participant == nil {
		return ""
	}
	if address := normalizeAddress(participant.Address); address != "" {
		key := "addr:" + address
		if participant.Lat != 0 || participant.Lng != 0 {
			key += "|coords:" + coordinateKey(models.RoundCoordinate(participant.Lat), models.RoundCoordinate(participant.Lng))
		}
		return key
	}
	return coordinateKey(models.RoundCoordinate(participant.Lat), models.RoundCoordinate(participant.Lng))
}

func participantGroupKey(group *participantGroup) string {
	if group == nil {
		return ""
	}
	if len(group.members) > 0 {
		if group.key != "" {
			return group.key
		}
		return householdKey(group.members[0])
	}
	if address := normalizeAddress(group.address); address != "" {
		return "addr:" + address
	}
	return coordinateKey(group.lat, group.lng)
}

func (rc routeContext) newParticipantGroup(participant *models.Participant) *participantGroup {
	return &participantGroup{
		members: []*models.Participant{participant},
		key:     rc.householdKey(participant),
		address: participant.Address,
		lat:     models.RoundCoordinate(participant.Lat),
		lng:     models.RoundCoordinate(participant.Lng),
	}
}

type householdBlock struct {
	start, end int
}

func (rc routeContext) routeHouseholdBlocks(stops []*models.Participant) []householdBlock {
	if len(stops) == 0 {
		return nil
	}

	blocks := make([]householdBlock, 0, len(stops))
	start := 0
	for i := 1; i <= len(stops); i++ {
		if i == len(stops) || rc.householdKey(stops[start]) != rc.householdKey(stops[i]) {
			blocks = append(blocks, householdBlock{start: start, end: i})
			start = i
		}
	}

	return blocks
}

func (rc routeContext) householdBoundaryPositions(stops []*models.Participant) []int {
	positions := make([]int, 0, len(stops)+1)
	positions = append(positions, 0)
	for i := 1; i < len(stops); i++ {
		if rc.householdKey(stops[i-1]) != rc.householdKey(stops[i]) {
			positions = append(positions, i)
		}
	}
	if len(stops) > 0 {
		positions = append(positions, len(stops))
	}

	return positions
}

func (rc routeContext) coalesceHouseholdStops(stops []*models.Participant) []*models.Participant {
	if len(stops) < 2 {
		return stops
	}

	orderedKeys := make([]string, 0, len(stops))
	grouped := make(map[string]*participantGroup, len(stops))
	for _, stop := range stops {
		key := rc.householdKey(stop)
		if group, exists := grouped[key]; exists {
			group.members = append(group.members, stop)
			continue
		}

		orderedKeys = append(orderedKeys, key)
		grouped[key] = rc.newParticipantGroup(stop)
	}

	result := make([]*models.Participant, 0, len(stops))
	for _, key := range orderedKeys {
		result = append(result, grouped[key].members...)
	}

	if slices.Equal(result, stops) {
		return stops
	}

	return result
}

func assignmentPreservesCapacityFeasibility(ctx context.Context, routes map[int64]*balancedRoute, currentDriverID int64, groups []*participantGroup, assignedGroupIndex, assignedCount int, splittableHouseholds map[string]struct{}) (bool, error) {
	select {
	case <-ctx.Done():
		return false, ctx.Err()
	default:
	}

	capacities := make([]int, 0, len(routes))
	totalCapacity := 0
	for driverID, route := range routes {
		capacity := route.driver.VehicleCapacity - len(route.stops)
		if driverID == currentDriverID {
			capacity -= assignedCount
		}
		if capacity < 0 {
			return false, nil
		}
		capacities = append(capacities, capacity)
		totalCapacity += capacity
	}

	remainingParticipants := 0
	atomicSizes := make([]int, 0, len(groups))
	for groupIdx, group := range groups {
		size := len(group.members)
		if groupIdx == assignedGroupIndex {
			size -= assignedCount
		}
		if size <= 0 {
			continue
		}

		remainingParticipants += size
		if len(splittableHouseholds) == 0 {
			atomicSizes = append(atomicSizes, size)
		} else if _, splittable := splittableHouseholds[participantGroupKey(group)]; !splittable {
			atomicSizes = append(atomicSizes, size)
		}
	}
	if remainingParticipants > totalCapacity {
		return false, nil
	}

	allSingletons := len(atomicSizes) > 0 && len(atomicSizes)+1 <= maxHouseholdPackingSearchNodes
	for _, size := range atomicSizes {
		if size != 1 {
			allSingletons = false
			break
		}
	}
	if allSingletons {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		return true, nil
	}
	return canPackAtomicGroupSizes(ctx, atomicSizes, capacities)
}

type householdPackingState struct {
	groupIndex          int
	remainingCapacities string
}

func canPackAtomicGroupSizes(ctx context.Context, groupSizes []int, capacities []int) (bool, error) {
	if len(groupSizes) == 0 {
		return true, nil
	}

	groupSizes = slices.Clone(groupSizes)
	capacities = slices.Clone(capacities)
	sort.Sort(sort.Reverse(sort.IntSlice(groupSizes)))
	sort.Sort(sort.Reverse(sort.IntSlice(capacities)))

	failedStates := make(map[householdPackingState]struct{})
	searchNodes := 0
	var pack func(int) (bool, error)
	pack = func(groupIndex int) (bool, error) {
		select {
		case <-ctx.Done():
			return false, ctx.Err()
		default:
		}

		searchNodes++
		if searchNodes > maxHouseholdPackingSearchNodes {
			return false, nil
		}
		if groupIndex == len(groupSizes) {
			return true, nil
		}

		state := householdPackingState{
			groupIndex:          groupIndex,
			remainingCapacities: normalizedCapacitiesKey(capacities),
		}
		if _, failed := failedStates[state]; failed {
			return false, nil
		}

		size := groupSizes[groupIndex]
		lastTriedCapacity := -1
		for i, capacity := range capacities {
			if capacity < size || capacity == lastTriedCapacity {
				continue
			}

			capacities[i] -= size
			packed, err := pack(groupIndex + 1)
			capacities[i] += size
			if err != nil {
				return false, err
			}
			if packed {
				return true, nil
			}
			lastTriedCapacity = capacity
		}

		failedStates[state] = struct{}{}
		return false, nil
	}

	return pack(0)
}

func normalizedCapacitiesKey(capacities []int) string {
	normalized := slices.Clone(capacities)
	sort.Sort(sort.Reverse(sort.IntSlice(normalized)))
	key := make([]byte, 0, len(normalized)*3)
	for _, capacity := range normalized {
		key = strconv.AppendInt(key, int64(capacity), 10)
		key = append(key, ',')
	}
	return string(key)
}
