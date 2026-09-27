package main

import (
	"encoding/json"
	"fmt"
	"hash/fnv"
	"net/http"
)

func syntheticPoint(address string) (float64, float64) {
	h := fnv.New32a()
	_, _ = h.Write([]byte(address))
	v := h.Sum32()
	return 35.96 + float64(v%100)/10000, -78.95 + float64((v/100)%100)/10000
}

func google(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/maps/api/geocode/json":
		address := r.URL.Query().Get("address")
		lat, lng := syntheticPoint(address)
		writeJSON(w, map[string]any{"status": "OK", "results": []any{map[string]any{"formatted_address": address, "geometry": map[string]any{"location_type": "ROOFTOP", "location": map[string]float64{"lat": lat, "lng": lng}}}}})
	case r.Method == http.MethodPost && r.URL.Path == "/v1/places:autocomplete":
		var body struct {
			Input string `json:"input"`
		}
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body) != nil {
			http.Error(w, "invalid body", http.StatusBadRequest)
			return
		}
		writeJSON(w, map[string]any{"suggestions": []any{map[string]any{"placePrediction": map[string]any{"placeId": "synthetic", "text": map[string]string{"text": body.Input}, "structuredFormat": map[string]any{"mainText": map[string]string{"text": body.Input}, "secondaryText": map[string]string{"text": "Synthetic local address"}}}}}})
	case r.Method == http.MethodPost && r.URL.Path == "/directions/v2:computeRoutes":
		var body struct {
			Intermediates []json.RawMessage `json:"intermediates"`
		}
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&body) != nil {
			http.Error(w, "invalid body", http.StatusBadRequest)
			return
		}
		legs := make([]any, len(body.Intermediates)+1)
		for i := range legs {
			legs[i] = map[string]any{"distanceMeters": 1000 + i*100, "duration": fmt.Sprintf("%ds", 120+i*12)}
		}
		writeJSON(w, map[string]any{"routes": []any{map[string]any{"legs": legs}}})
	default:
		http.NotFound(w, r)
	}
}
