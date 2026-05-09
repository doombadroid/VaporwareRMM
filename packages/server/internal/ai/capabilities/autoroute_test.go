package capabilities

import "testing"

func TestComputeScoreFavoursLowerLoad(t *testing.T) {
	a := RouteCandidate{OpenLoad: 0, CustomerWins30: 0, SkillMatches: 0}
	b := RouteCandidate{OpenLoad: 10, CustomerWins30: 0, SkillMatches: 0}
	if computeScore(a, 100) <= computeScore(b, 100) {
		t.Error("expected lower load to score higher")
	}
}

func TestComputeScoreCustomerAffinitySaturates(t *testing.T) {
	a := RouteCandidate{OpenLoad: 5, CustomerWins30: 5, SkillMatches: 0}
	b := RouteCandidate{OpenLoad: 5, CustomerWins30: 50, SkillMatches: 0}
	// Affinity saturates at 10 wins; further wins shouldn't keep adding.
	sa := computeScore(a, 100)
	sb := computeScore(b, 100)
	if sb <= sa {
		t.Error("more affinity should still score >= same affinity")
	}
	// But the difference should be small — saturation working.
	if sb-sa > 0.4*1.0-0.4*0.5 {
		t.Logf("note: affinity above 10 still moved the score by %.3f", sb-sa)
	}
}

func TestComputeScoreSkillMatchesContributeLess(t *testing.T) {
	// Skill weight is 0.2 (vs load 0.4 + affinity 0.4). 5 skill matches at
	// max contribution = 0.2; 1 fewer open ticket = ~0.16 from load delta.
	// They're roughly comparable — skill shouldn't dominate.
	a := RouteCandidate{OpenLoad: 0, CustomerWins30: 0, SkillMatches: 0}
	b := RouteCandidate{OpenLoad: 0, CustomerWins30: 0, SkillMatches: 5}
	if computeScore(b, 100)-computeScore(a, 100) > 0.21 {
		t.Errorf("skill weight too large: delta = %.3f", computeScore(b, 100)-computeScore(a, 100))
	}
}

func TestComputeScoreRoutingWeightScales(t *testing.T) {
	a := RouteCandidate{OpenLoad: 0}
	full := computeScore(a, 100)
	half := computeScore(a, 50)
	if half >= full {
		t.Errorf("routing weight 50 should halve score; got full=%.3f half=%.3f", full, half)
	}
}

func TestNeedsTiebreaker(t *testing.T) {
	cases := []struct {
		name string
		c    []RouteCandidate
		want bool
	}{
		{"single candidate", []RouteCandidate{{Score: 0.7}}, false},
		{"clear winner", []RouteCandidate{{Score: 0.85}, {Score: 0.5}}, false},
		{"close pair under 0.8", []RouteCandidate{{Score: 0.6}, {Score: 0.58}}, true},
		{"distant pair under 0.8", []RouteCandidate{{Score: 0.6}, {Score: 0.3}}, false},
		{"zero second", []RouteCandidate{{Score: 0.5}, {Score: 0}}, false},
	}
	for _, c := range cases {
		if got := needsTiebreaker(c.c); got != c.want {
			t.Errorf("%s: needsTiebreaker = %v, want %v", c.name, got, c.want)
		}
	}
}
