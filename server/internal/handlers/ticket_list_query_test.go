package handlers

import "testing"

func TestTicketListRawQueryIsStrictAndBounded(t *testing.T) {
	valid := []string{
		"",
		"page=1&page_size=25&sort_by=created_at&sort_order=desc",
		"status=open%2Cin_progress&priority=high&assigned_to=9",
		"sla_breached=true&is_overdue=false&unassigned=true",
		"filter=%7B%22tags%22%3A%5B%22incident%22%2C%22vip%22%5D%7D",
	}
	for _, query := range valid {
		if err := validateTicketListRawQuery(query); err != nil {
			t.Errorf("valid query %q rejected: %v", query, err)
		}
	}

	invalid := []string{
		"page=0",
		"page_size=101",
		"limit=25",
		"page=1&page=2",
		"sort_by=unknown",
		"sort_order=DESC",
		"assigned_to=0",
		"sla_breached=1",
		"status=unknown",
		"priority=super",
		"search=%20incident",
		"filter=%7B%22unknown%22%3Atrue%7D",
		"filter=%7B%22type%22%3A%22unknown%22%7D",
		"filter=%7B%22source%22%3A%22unknown%22%7D",
		"filter=%7B%22tags%22%3A%5B1%5D%7D",
		"filter=%7B%22sla_breached%22%3A%22yes%22%7D",
		"filter=%ZZ",
	}
	for _, query := range invalid {
		if err := validateTicketListRawQuery(query); err == nil {
			t.Errorf("invalid query %q was accepted", query)
		}
	}
}
