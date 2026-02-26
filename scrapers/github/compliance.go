package github

type ComplianceMapping struct {
	SOC2     []string `json:"soc2,omitempty"`
	NISTSSDF []string `json:"nist_ssdf,omitempty"`
	CIS      []string `json:"cis,omitempty"`
}

var complianceMappings = map[string]ComplianceMapping{
	"Binary-Artifacts":      {SOC2: []string{"CC6.1", "CC7.1"}, NISTSSDF: []string{"PO.3.1", "PS.1.1"}, CIS: []string{"2.3", "7.1"}},
	"Branch-Protection":     {SOC2: []string{"CC6.1", "CC6.6", "CC7.2"}, NISTSSDF: []string{"PO.5.1", "PS.3.1"}, CIS: []string{"5.3", "16.2"}},
	"CI-Tests":              {SOC2: []string{"CC8.1"}, NISTSSDF: []string{"PW.8.1", "RV.1.1"}, CIS: []string{"16.5"}},
	"CII-Best-Practices":   {SOC2: []string{"CC6.1", "CC7.1", "CC8.1"}, NISTSSDF: []string{"PO.3.1"}, CIS: []string{}},
	"Code-Review":           {SOC2: []string{"CC6.1", "CC8.1"}, NISTSSDF: []string{"PS.2.1", "RV.1.1"}, CIS: []string{"16.2"}},
	"Contributors":          {SOC2: []string{"CC6.1"}, NISTSSDF: []string{}, CIS: []string{}},
	"Dangerous-Workflow":    {SOC2: []string{"CC6.1", "CC6.6", "CC7.2"}, NISTSSDF: []string{}, CIS: []string{}},
	"Dependency-Update-Tool": {SOC2: []string{"CC6.1", "CC7.1"}, NISTSSDF: []string{"PO.3.2", "PS.1.1"}, CIS: []string{"7.1", "7.2"}},
	"Fuzzing":               {SOC2: []string{"CC6.1", "CC8.1"}, NISTSSDF: []string{"PW.7.1", "RV.1.2"}, CIS: []string{"16.5"}},
	"License":               {SOC2: []string{"CC1.2"}, NISTSSDF: []string{}, CIS: []string{}},
	"Maintained":            {SOC2: []string{"CC6.1", "CC9.2"}, NISTSSDF: []string{"PO.3.3"}, CIS: []string{}},
	"Packaging":             {SOC2: []string{"CC6.1", "CC7.1"}, NISTSSDF: []string{}, CIS: []string{}},
	"Pinned-Dependencies":   {SOC2: []string{"CC6.1", "CC7.1"}, NISTSSDF: []string{"PO.3.2", "PS.1.1"}, CIS: []string{}},
	"SAST":                  {SOC2: []string{"CC6.1", "CC6.8", "CC7.1"}, NISTSSDF: []string{"PW.7.1", "RV.1.1"}, CIS: []string{"16.2", "16.5"}},
	"Security-Policy":       {SOC2: []string{"CC1.2", "CC6.1"}, NISTSSDF: []string{"PO.5.1"}, CIS: []string{"17.1"}},
	"Signed-Releases":       {SOC2: []string{"CC6.1", "CC6.7", "CC7.1"}, NISTSSDF: []string{"PS.3.1", "PS.3.2"}, CIS: []string{"2.3", "10.5"}},
	"Token-Permissions":     {SOC2: []string{"CC6.1", "CC6.2"}, NISTSSDF: []string{"PO.5.1", "PS.3.1"}, CIS: []string{"5.3", "6.1"}},
	"Vulnerabilities":       {SOC2: []string{"CC6.1", "CC7.1", "CC9.2"}, NISTSSDF: []string{"PO.3.2", "RV.1.1"}, CIS: []string{"7.2", "7.3"}},
	"Webhooks":              {SOC2: []string{"CC6.1"}, NISTSSDF: []string{}, CIS: []string{}},
}

func GetComplianceMappings(checkName string) ComplianceMapping {
	mapping, ok := complianceMappings[checkName]
	if !ok {
		return ComplianceMapping{}
	}
	return ComplianceMapping{
		SOC2:     append([]string(nil), mapping.SOC2...),
		NISTSSDF: append([]string(nil), mapping.NISTSSDF...),
		CIS:      append([]string(nil), mapping.CIS...),
	}
}
