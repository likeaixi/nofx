package spider

import (
	"testing"
)

// 辅助构造函数：一条 SSPHist 记录
func makeSSPSnapshot(t int64, ssp []float64) map[string]any {
	return map[string]any{
		"T":   t,
		"SSP": ssp,
	}
}

func TestComputeSSPBoxDrift_FlatBox_RealData(t *testing.T) {
	P := 89319.55

	SSPHist := []map[string]any{
		makeSSPSnapshot(1766422980000, []float64{
			84700.49,
			88400.78,
			88500.82,
			88600.66,
			88700.95,
			88900.04,
			88900.52,
			89000.18,
			89300.26,
			89300.41,
			89400.47,
			89500.25,
			89500.36,
			89500.39,
			89600.92,
			89700.68,
			90000.21,
			91300.15,
			91600.51,
		}),
		makeSSPSnapshot(1766423040000, []float64{
			84700.49,
			88400.78,
			88500.82,
			88600.66,
			88700.95,
			88900.04,
			88900.52,
			89100.18,
			89300.26,
			89300.41,
			89400.47,
			89500.25,
			89500.36,
			89500.39,
			89600.92,
			89700.68,
			90000.21,
			91300.15,
			91600.51,
		}),
	}

	got := ComputeSSPBoxDrift(P, SSPHist)
	want := "FLAT_BOX"

	if got != want {
		t.Fatalf("ComputeSSPBoxDrift() = %q, want %q", got, want)
	}
}

func TestComputeSSPBoxDrift_InsufficientHistory(t *testing.T) {
	P := 10000.0

	SSPHist := []map[string]any{
		makeSSPSnapshot(1, []float64{1000, 2000, 3000}),
	}

	got := ComputeSSPBoxDrift(P, SSPHist)
	if got != "" {
		t.Fatalf("ComputeSSPBoxDrift() with len(SSPHist)<2 = %q, want empty string", got)
	}
}

func TestComputeSSPBoxDrift_UpBox(t *testing.T) {
	// 构造一个特别简单的场景：
	// A: box 大致在 [1000, 1000]
	// B: 整体上移到 [1120, 1120]
	// dLow = +120, dHigh = +120 -> UP_BOX
	P := 1500.0

	SSPHist := []map[string]any{
		makeSSPSnapshot(1, []float64{
			1000, 2000,
		}),
		makeSSPSnapshot(2, []float64{
			1120, 2120,
		}),
	}

	got := ComputeSSPBoxDrift(P, SSPHist)
	want := "UP_BOX"

	if got != want {
		t.Fatalf("ComputeSSPBoxDrift() = %q, want %q", got, want)
	}
}

func TestComputeSSPBoxDrift_DownBox(t *testing.T) {
	P := 1500.0

	SSPHist := []map[string]any{
		// A: 盒子在 [1000,1100]
		makeSSPSnapshot(1, []float64{
			1000, 1100,
		}),
		// B: 整体下移 120 → [880, 980]
		makeSSPSnapshot(2, []float64{
			880, 980,
		}),
	}

	got := ComputeSSPBoxDrift(P, SSPHist)
	want := "DOWN_BOX"

	if got != want {
		t.Fatalf("ComputeSSPBoxDrift() = %q, want %q", got, want)
	}
}

func TestComputeSSPBoxDrift_FlatBox_SmallShift(t *testing.T) {
	// 即便有轻微移动，但未超过 120，就应该是 FLAT_BOX
	P := 1500.0

	SSPHist := []map[string]any{
		makeSSPSnapshot(1, []float64{
			1000, 2000,
		}),
		makeSSPSnapshot(2, []float64{
			1100, 2100, // 仅 +100
		}),
	}

	got := ComputeSSPBoxDrift(P, SSPHist)
	want := "FLAT_BOX"

	if got != want {
		t.Fatalf("ComputeSSPBoxDrift() = %q, want %q", got, want)
	}
}
