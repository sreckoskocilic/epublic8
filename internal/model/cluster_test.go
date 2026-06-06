package model

import (
	"strings"
	"testing"
)

func makeObs(texts []string, heights []float64) []visionObs {
	obs := make([]visionObs, len(texts))
	for i := range texts {
		obs[i] = visionObs{T: texts[i], X: 0.1, Y: 0.5, W: 0.5, H: heights[i]}
	}
	return obs
}

func TestClusterObservationsEmpty(t *testing.T) {
	r := ClusterObservations(nil, nil)
	if len(r.Clusters) != 0 {
		t.Error("expected no clusters for nil input")
	}
	r = ClusterObservations([][]visionObs{{}}, nil)
	if len(r.Clusters) != 0 {
		t.Error("expected no clusters for empty obs")
	}
}

func TestClusterObservationsUniform(t *testing.T) {
	obs := makeObs(
		[]string{"Line one", "Line two", "Line three", "Line four", "Line five", "Line six"},
		[]float64{0.020, 0.020, 0.021, 0.020, 0.021, 0.020},
	)
	r := ClusterObservations([][]visionObs{obs}, nil)
	if len(r.Clusters) != 1 {
		t.Errorf("expected 1 cluster for uniform H, got %d", len(r.Clusters))
	}
	if r.Clusters[0].RelativeSize != "body" {
		t.Errorf("single cluster should be labeled body, got %q", r.Clusters[0].RelativeSize)
	}
}

func TestClusterObservationsBimodal(t *testing.T) {
	obs := makeObs(
		[]string{
			"Body one", "Body two", "Body three", "Body four", "Body five",
			"Body six", "Body seven", "Body eight", "Body nine", "Body ten",
			"Caption A", "Caption B", "Caption C", "Caption D", "Caption E",
		},
		[]float64{
			0.020, 0.021, 0.020, 0.019, 0.020,
			0.021, 0.020, 0.019, 0.020, 0.021,
			0.012, 0.011, 0.012, 0.013, 0.012,
		},
	)
	r := ClusterObservations([][]visionObs{obs}, nil)
	if len(r.Clusters) < 2 {
		t.Errorf("expected ≥2 clusters for bimodal H, got %d", len(r.Clusters))
	}

	hasBody := false
	hasCaption := false
	for _, c := range r.Clusters {
		if c.RelativeSize == "body" {
			hasBody = true
		}
		if c.RelativeSize == "caption" || c.RelativeSize == "metadata" {
			hasCaption = true
		}
	}
	if !hasBody {
		t.Error("expected a body cluster")
	}
	if !hasCaption {
		t.Error("expected a caption/metadata cluster")
	}
}

func TestClusterObservationsThreeTier(t *testing.T) {
	var obs []visionObs
	// Headings
	for i := 0; i < 5; i++ {
		obs = append(obs, visionObs{T: "HEADING", X: 0.1, Y: 0.9, W: 0.5, H: 0.035})
	}
	// Body
	for i := 0; i < 30; i++ {
		obs = append(obs, visionObs{T: "Body text line", X: 0.1, Y: 0.5, W: 0.8, H: 0.020})
	}
	// Captions
	for i := 0; i < 10; i++ {
		obs = append(obs, visionObs{T: "Caption info", X: 0.1, Y: 0.2, W: 0.4, H: 0.012})
	}

	r := ClusterObservations([][]visionObs{obs}, nil)
	if len(r.Clusters) < 3 {
		t.Errorf("expected ≥3 clusters for three-tier H, got %d", len(r.Clusters))
	}

	labels := map[string]bool{}
	for _, c := range r.Clusters {
		labels[c.RelativeSize] = true
	}
	if !labels["heading"] {
		t.Error("expected heading cluster")
	}
	if !labels["body"] {
		t.Error("expected body cluster")
	}
}

func TestClusterObservationsCappedAt6(t *testing.T) {
	var obs []visionObs
	// Create 10 distinct tiers
	for tier := 0; tier < 10; tier++ {
		h := 0.010 + float64(tier)*0.005
		for i := 0; i < 20; i++ {
			obs = append(obs, visionObs{T: "text", X: 0.1, Y: 0.5, W: 0.5, H: h})
		}
	}
	r := ClusterObservations([][]visionObs{obs}, nil)
	if len(r.Clusters) > maxClusters {
		t.Errorf("expected ≤%d clusters, got %d", maxClusters, len(r.Clusters))
	}
}

func TestClusterObservationsSortedDescending(t *testing.T) {
	obs := makeObs(
		[]string{"Small", "Small", "Small", "Small", "Small", "Big", "Big", "Big", "Big", "Big"},
		[]float64{0.010, 0.010, 0.010, 0.010, 0.010, 0.030, 0.030, 0.030, 0.030, 0.030},
	)
	r := ClusterObservations([][]visionObs{obs}, nil)
	for i := 1; i < len(r.Clusters); i++ {
		if r.Clusters[i].AvgH > r.Clusters[i-1].AvgH {
			t.Error("clusters not sorted descending by AvgH")
		}
	}
}

func TestClusterObservationsIDsSequential(t *testing.T) {
	obs := makeObs(
		[]string{"A", "A", "A", "A", "A", "B", "B", "B", "B", "B"},
		[]float64{0.010, 0.010, 0.010, 0.010, 0.010, 0.030, 0.030, 0.030, 0.030, 0.030},
	)
	r := ClusterObservations([][]visionObs{obs}, nil)
	for i, c := range r.Clusters {
		if c.ID != i+1 {
			t.Errorf("cluster %d has ID %d, expected %d", i, c.ID, i+1)
		}
	}
}

func TestClusterObservationsFingerprint(t *testing.T) {
	var obs []visionObs
	for i := 0; i < 5; i++ {
		obs = append(obs, visionObs{T: "ITALIAN RENAISSANCE", X: 0.1, Y: 0.9, W: 0.5, H: 0.035})
	}
	for i := 0; i < 30; i++ {
		obs = append(obs, visionObs{T: "Body text", X: 0.1, Y: 0.5, W: 0.8, H: 0.020})
	}

	r := ClusterObservations([][]visionObs{obs}, nil)
	if r.Fingerprint == "" {
		t.Error("expected non-empty fingerprint")
	}
	if !strings.Contains(r.Fingerprint, "italian renaissance") {
		t.Errorf("fingerprint should contain heading text, got %q", r.Fingerprint)
	}
}

func TestClassifyObs(t *testing.T) {
	clusters := []FontCluster{
		{ID: 1, MinH: 0.025, MaxH: 0.040, AvgH: 0.033, LineCount: 0, RelativeSize: "", SampleTexts: nil},
		{ID: 2, MinH: 0.017, MaxH: 0.024, AvgH: 0.020, LineCount: 0, RelativeSize: "", SampleTexts: nil},
		{ID: 3, MinH: 0.008, MaxH: 0.016, AvgH: 0.012, LineCount: 0, RelativeSize: "", SampleTexts: nil},
	}

	tests := []struct {
		h    float64
		want int
	}{
		{0.035, 1},
		{0.020, 2},
		{0.012, 3},
		{0.050, 1}, // above all ranges → nearest
		{0.005, 3}, // below all ranges → nearest
	}
	for _, tt := range tests {
		o := visionObs{T: "x", X: 0, Y: 0, W: 0, H: tt.h}
		got := classifyObs(o, clusters)
		if got != tt.want {
			t.Errorf("classifyObs(H=%.3f) = %d, want %d", tt.h, got, tt.want)
		}
	}
}

func TestFilterObsByCluster(t *testing.T) {
	obs := []visionObs{
		{T: "The Italian Renaissance was a golden age", X: 0, Y: 0, W: 0, H: 0.020},
		{T: "of art and culture that transformed Europe", X: 0, Y: 0, W: 0, H: 0.021},
		{T: "Caption text about the painting", X: 0, Y: 0, W: 0, H: 0.012},
		{T: "and produced some of the greatest masterpieces", X: 0, Y: 0, W: 0, H: 0.020},
	}
	text := strings.Join([]string{obs[0].T, obs[1].T, obs[2].T, obs[3].T}, "\n")
	clusters := []FontCluster{
		{ID: 1, MinH: 0.017, MaxH: 0.024, AvgH: 0.020, LineCount: 0, RelativeSize: "", SampleTexts: nil},
		{ID: 2, MinH: 0.008, MaxH: 0.016, AvgH: 0.012, LineCount: 0, RelativeSize: "", SampleTexts: nil},
	}

	filtered, filteredObs := FilterObsByCluster(text, obs, []int{1}, clusters)
	if strings.Contains(filtered, "Caption") {
		t.Error("caption should be filtered out by cluster")
	}
	if !strings.Contains(filtered, "Italian Renaissance") || !strings.Contains(filtered, "greatest masterpieces") {
		t.Error("body text should be kept")
	}
	if len(filteredObs) != 3 {
		t.Errorf("expected 3 filtered obs, got %d", len(filteredObs))
	}
	lines := strings.Split(filtered, "\n")
	if len(lines) != len(filteredObs) {
		t.Error("filtered text/obs 1:1 alignment broken")
	}
}

func TestFilterObsByClusterMismatch(t *testing.T) {
	obs := []visionObs{{T: "A", X: 0, Y: 0, W: 0, H: 0.02}}
	text := "A\nB" // mismatch
	got, gotObs := FilterObsByCluster(text, obs, []int{1}, nil)
	if got != text || len(gotObs) != len(obs) {
		t.Error("should return unchanged on mismatch")
	}
}

func TestNormalizeFingerprint(t *testing.T) {
	got := normalizeFingerprint("  ITALIAN   RENAISSANCE!!!  ")
	if got != "italian renaissance" {
		t.Errorf("got %q", got)
	}
}
