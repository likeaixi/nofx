package spider

import (
	"math"
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
	P := 88323.7

	SSPHist := []map[string]any{
		makeSSPSnapshot(1766422980000, []float64{84700.49, 88300.25, 88300.82, 88300.89, 88400.77, 88400.78, 88400.92, 88600.04, 88700.68, 88800.18, 89000.26, 89500.39, 89900.21, 91300.15}),
		makeSSPSnapshot(1766423040000, []float64{84700.49, 88200.78, 88300.25, 88300.82, 88400.77, 88400.92, 88600.04, 88700.52, 88800.18, 88900.59, 89000.26, 89500.39, 89900.21, 91300.15}),
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

// 帮助比较 float64
func almostEqual(a, b float64, eps float64) bool {
	return math.Abs(a-b) <= eps
}

func TestBuildSuperBox_SimpleSingleBand_Battle(t *testing.T) {
	// SSP 连续、间隔 < 120 → 只有一个 band
	SSP := []float64{1000, 1050, 1100}
	P := 1050.0

	box, err := BuildSuperBox(P, SSP)
	if err != nil {
		t.Fatalf("BuildSuperBox error: %v", err)
	}

	if box.BoxLow != 1000 || box.BoxHigh != 1100 {
		t.Fatalf("Box range = [%d,%d], want [1000,1100]", box.BoxLow, box.BoxHigh)
	}
	if box.BoxWidth != 100 {
		t.Fatalf("BoxWidth = %d, want 100", box.BoxWidth)
	}
	if box.Zone != "BATTLE" {
		t.Fatalf("Zone = %q, want %q", box.Zone, "BATTLE")
	}
	if box.PosBox == nil {
		t.Fatalf("PosBox is nil, want non-nil")
	}
	if !almostEqual(*box.PosBox, 0.5, 1e-9) {
		t.Fatalf("PosBox = %f, want ~0.5", *box.PosBox)
	}

	if box.S0 == nil || *box.S0 != 1050 {
		t.Fatalf("S0 = %v, want 1050", box.S0)
	}
	if box.R0 == nil || *box.R0 != 1100 {
		t.Fatalf("R0 = %v, want 1100", box.R0)
	}
}

func TestBuildSuperBox_EdgeZones(t *testing.T) {
	SSP := []float64{1000, 1050, 1100}

	// 接近下边缘 → EDGE_LOW
	boxLowEdge, err := BuildSuperBox(1001.0, SSP)
	if err != nil {
		t.Fatalf("BuildSuperBox low-edge error: %v", err)
	}
	if boxLowEdge.Zone != "EDGE_LOW" {
		t.Fatalf("Zone(low) = %q, want %q", boxLowEdge.Zone, "EDGE_LOW")
	}

	// 接近上边缘 → EDGE_HIGH
	boxHighEdge, err := BuildSuperBox(1099.0, SSP)
	if err != nil {
		t.Fatalf("BuildSuperBox high-edge error: %v", err)
	}
	if boxHighEdge.Zone != "EDGE_HIGH" {
		t.Fatalf("Zone(high) = %q, want %q", boxHighEdge.Zone, "EDGE_HIGH")
	}
}

func TestBuildSuperBox_OutZones(t *testing.T) {
	SSP := []float64{1000, 1050, 1100}

	// P 在 box 下方 → OUT_DOWN, PosBox=nil
	below, err := BuildSuperBox(900.0, SSP)
	if err != nil {
		t.Fatalf("BuildSuperBox below error: %v", err)
	}
	if below.Zone != "OUT_DOWN" {
		t.Fatalf("Zone(below) = %q, want %q", below.Zone, "OUT_DOWN")
	}
	if below.PosBox != nil {
		t.Fatalf("PosBox(below) = %v, want nil", *below.PosBox)
	}

	// P 在 box 上方 → OUT_UP, PosBox=nil
	above, err := BuildSuperBox(1200.0, SSP)
	if err != nil {
		t.Fatalf("BuildSuperBox above error: %v", err)
	}
	if above.Zone != "OUT_UP" {
		t.Fatalf("Zone(above) = %q, want %q", above.Zone, "OUT_UP")
	}
	if above.PosBox != nil {
		t.Fatalf("PosBox(above) = %v, want nil", *above.PosBox)
	}
}

func TestBuildSuperBox_MultiBand_Expansion(t *testing.T) {
	// 两个 band，价格间隔 400：
	//  - gap >= 120 → 分成两个 segment
	//  - gap > 300 → 合并段时分成两个 band
	//  - expandSuperBox 时 gapBand=400 <= 900 → 会把两个 band 合并成一个 super-box
	SSP := []float64{1000, 1400}
	P := 1200.0

	box, err := BuildSuperBox(P, SSP)
	if err != nil {
		t.Fatalf("BuildSuperBox error: %v", err)
	}

	if box.BoxLow != 1000 || box.BoxHigh != 1400 {
		t.Fatalf("Box range = [%d,%d], want [1000,1400]", box.BoxLow, box.BoxHigh)
	}
	if box.BoxWidth != 400 {
		t.Fatalf("BoxWidth = %d, want 400", box.BoxWidth)
	}
	if box.Zone == "OUT_UP" || box.Zone == "OUT_DOWN" {
		t.Fatalf("Zone = %q, want in-box zone (BATTLE/EDGE_LOW/EDGE_HIGH)", box.Zone)
	}
	if box.PosBox == nil {
		t.Fatalf("PosBox is nil, want non-nil")
	}
}

func TestBuildSuperBox_EmptySSP(t *testing.T) {
	_, err := BuildSuperBox(1000.0, []float64{})
	if err == nil {
		t.Fatalf("BuildSuperBox with empty SSP, want error, got nil")
	}
}

func TestBuildSuperBox(t *testing.T) {
	SSP := []float64{
		84700.49,
		88300.66,
		88300.89,
		88400.77,
		88400.78,
		88400.92,
		88500.25,
		88500.82,
		88600.04,
		88700.52,
		88800.18,
		88900.59,
		89000.26,
		89500.39,
		89900.21,
		91300.15}

	P := 88328.4

	box, err := BuildSuperBox(P, SSP)
	if err != nil {
		t.Fatalf("BuildSuperBox error %v", err)
	}

	t.Logf("Box zone %v", box.Zone)
}
