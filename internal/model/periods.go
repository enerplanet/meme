// Copyright (c) 2026 BigGeoData & Spatial AI, Technische Hochschule Deggendorf
// SPDX-License-Identifier: MIT

package model

// InvestmentPeriod is one period of a multi-period plan with its defaults
// applied (ADR-0011): the year, the years it spans (length_years, else the
// gap to the next period, else the previous span, else 1) and its objective
// weight (default 1).
type InvestmentPeriod struct {
	Name      string
	Year      int
	Years     float64
	Objective float64
}

// InvestmentPeriods returns the model's periods as investment periods when
// every period carries a year; nil otherwise (a single horizon).
func (m *Model) InvestmentPeriods() []InvestmentPeriod {
	if len(m.Periods) == 0 {
		return nil
	}
	for _, p := range m.Periods {
		if p.Year == nil {
			return nil
		}
	}
	out := make([]InvestmentPeriod, len(m.Periods))
	for i, p := range m.Periods {
		ip := InvestmentPeriod{Name: p.Name, Year: *p.Year, Years: 1, Objective: 1}
		switch {
		case p.LengthYears != nil:
			ip.Years = *p.LengthYears
		case i+1 < len(m.Periods):
			ip.Years = float64(*m.Periods[i+1].Year - *p.Year)
		case i > 0:
			ip.Years = out[i-1].Years
		}
		if p.ObjectiveWeight != nil {
			ip.Objective = *p.ObjectiveWeight
		}
		out[i] = ip
	}
	return out
}
