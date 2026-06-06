package model

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestProfileSaveAndMatch(t *testing.T) {
	dir := t.TempDir()
	store, err := NewProfileStore(dir)
	if err != nil {
		t.Fatal(err)
	}

	p := Profile{
		Fingerprint: "italian renaissance baroque gothic art impressionism",
		Title:       "Art History Book",
		Clusters: []FontCluster{
			{ID: 1, MinH: 0.025, MaxH: 0.040, AvgH: 0.033, LineCount: 15, RelativeSize: "heading", SampleTexts: []string{"ITALIAN RENAISSANCE"}},
			{ID: 2, MinH: 0.017, MaxH: 0.024, AvgH: 0.020, LineCount: 200, RelativeSize: "body", SampleTexts: []string{"Body text"}},
		},
		SelectedIDs: []int{1, 2},
		CreatedAt:   time.Now(),
		LastUsedAt:  time.Now(),
	}

	if err := store.Save(p); err != nil {
		t.Fatal(err)
	}

	// Exact fingerprint match.
	found, ok := store.Match("italian renaissance baroque gothic art impressionism")
	if !ok || found == nil {
		t.Fatal("expected match for exact fingerprint")
	}
	if found.Title != "Art History Book" {
		t.Errorf("got title %q", found.Title)
	}
}

func TestProfileFuzzyMatch(t *testing.T) {
	dir := t.TempDir()
	store, err := NewProfileStore(dir)
	if err != nil {
		t.Fatal(err)
	}

	p := Profile{
		Fingerprint: "italian renaissance baroque gothic art impressionism modern",
		Title:       "Art Book",
		Clusters:    nil,
		SelectedIDs: []int{1, 2},
		CreatedAt:   time.Now(),
		LastUsedAt:  time.Now(),
	}
	if err := store.Save(p); err != nil {
		t.Fatal(err)
	}

	// Slightly different OCR — missing one word, one misspelled.
	found, ok := store.Match("italian renaissanse baroque gothic art impressionism")
	if !ok || found == nil {
		t.Fatal("expected fuzzy match")
	}
}

func TestProfileNoFalsePositive(t *testing.T) {
	dir := t.TempDir()
	store, err := NewProfileStore(dir)
	if err != nil {
		t.Fatal(err)
	}

	p := Profile{
		Fingerprint: "italian renaissance baroque gothic art impressionism",
		Title:       "Art Book",
		Clusters:    nil,
		SelectedIDs: []int{1},
		CreatedAt:   time.Now(),
		LastUsedAt:  time.Now(),
	}
	if err := store.Save(p); err != nil {
		t.Fatal(err)
	}

	// Completely different book.
	_, ok := store.Match("world history ancient civilizations mesopotamia egypt")
	if ok {
		t.Error("should not match completely different fingerprint")
	}
}

func TestProfileList(t *testing.T) {
	dir := t.TempDir()
	store, err := NewProfileStore(dir)
	if err != nil {
		t.Fatal(err)
	}

	for i, fp := range []string{"book one alpha", "book two beta", "book three gamma"} {
		p := Profile{
			Fingerprint: fp,
			Title:       fp,
			Clusters:    nil,
			SelectedIDs: []int{i + 1},
			CreatedAt:   time.Now(),
			LastUsedAt:  time.Now(),
		}
		if err := store.Save(p); err != nil {
			t.Fatal(err)
		}
	}

	profiles, err := store.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(profiles) != 3 {
		t.Errorf("expected 3 profiles, got %d", len(profiles))
	}
}

func TestProfileStoreCreatesDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "sub", "profiles")
	_, err := NewProfileStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		t.Error("profile dir not created")
	}
}

func TestWordSet(t *testing.T) {
	got := wordSet("the quick brown fox")
	if len(got) != 4 {
		t.Errorf("expected 4 words, got %d", len(got))
	}
	if !got["the"] || !got["quick"] || !got["brown"] || !got["fox"] {
		t.Error("wrong word set")
	}
}

func TestJaccardSimilarity(t *testing.T) {
	a := map[string]bool{"x": true, "y": true, "z": true}
	b := map[string]bool{"x": true, "y": true, "w": true}
	score := jaccardSimilarity(a, b)
	// intersection=2, union=4 → 0.5
	if score < 0.49 || score > 0.51 {
		t.Errorf("expected ~0.5, got %f", score)
	}
}
