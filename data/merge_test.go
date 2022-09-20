package data

import "testing"

func TestMergePoints(t *testing.T) {
	out := testTypeData

	modifiedDescription := "test type modified"

	mods := []Point{
		{Type: "description", Text: modifiedDescription},
	}

	err := MergePoints(out.ID, mods, &out)

	if err != nil {
		t.Fatal("Merge error: ", err)
	}

	if out.Description != modifiedDescription {
		t.Errorf("Description not modified, exp: %v, got: %v", modifiedDescription,
			out.Description)
	}

	// make sure other points did not get reset
}

func TestMergeEdgePoints(t *testing.T) {
	out := testTypeData

	modifiedRole := "user"

	mods := []Point{
		{Type: "role", Text: modifiedRole},
	}

	err := MergeEdgePoints(out.ID, out.Parent, mods, &out)

	if err != nil {
		t.Fatal("Merge error: ", err)
	}

	if out.Role != modifiedRole {
		t.Errorf("role not modified, exp: %v, got: %v", modifiedRole,
			out.Role)
	}
}

func TestMergeChildPoints(t *testing.T) {
	testData := testX{
		ID:          "ID-testX",
		Parent:      "ID-parent",
		Description: "test X node",
		TestYs: []testY{
			{ID: "ID-testY",
				Parent:      "ID-testX",
				Description: "test Y node",
				Count:       3,
				Role:        "",
				TestZs: []testZ{
					{
						ID:          "ID-testZ",
						Parent:      "ID-testY",
						Description: "test Z node",
						Count:       23,
						Role:        "peon",
					},
				},
			},
		},
	}

	modifiedDescription := "test type modified"

	mods := []Point{
		{Type: "description", Text: modifiedDescription},
	}

	err := MergePoints("ID-testY", mods, &testData)

	if err != nil {
		t.Fatal("Merge error: ", err)
	}

	if testData.TestYs[0].Description != modifiedDescription {
		t.Errorf("Description not modified, exp: %v, got: %v", modifiedDescription,
			testData.TestYs[0].Description)
	}

	// make sure other points did not get reset
	if testData.TestYs[0].Count != 3 {
		t.Errorf("Merge reset other data")
	}

	if testData.Description != "test X node" {
		t.Errorf("Top level node description modified when it should not have")
	}

	// modify description of Z point
	modifiedDescription = "test Z type modified"

	mods = []Point{
		{Type: "description", Text: modifiedDescription},
	}

	err = MergePoints("ID-testZ", mods, &testData)
	if err != nil {
		t.Fatal("Merge error: ", err)
	}

	if testData.TestYs[0].TestZs[0].Description != modifiedDescription {
		t.Errorf("Description not modified, exp: %v, got: %v", modifiedDescription,
			testData.TestYs[0].TestZs[0].Description)
	}

	// Test edge modifications
	modifiedRole := "yrole"

	mods = []Point{
		{Type: "role", Text: modifiedRole},
	}

	err = MergeEdgePoints("ID-testZ", "ID-testY", mods, &testData)
	if err != nil {
		t.Fatal("Merge error: ", err)
	}

	if testData.TestYs[0].TestZs[0].Role != modifiedRole {
		t.Errorf("Role not modified, exp: %v, got: %v", modifiedRole,
			testData.TestYs[0].TestZs[0].Role)
	}
}