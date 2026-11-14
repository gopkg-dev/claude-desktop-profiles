package profiles

import "time"

// profileDetails returns cached display details only after rechecking the directory's login.
func (m *Manager) profileDetails(
	view onlineView, id, dir, expectedAccount string, now time.Time,
) (*AccountDetails, string) {
	var details *AccountDetails
	var status string
	if m.detailsReader != nil {
		details, status = m.detailsReader(dir, expectedAccount, now)
	} else {
		details, status = view.details(id, expectedAccount)
	}
	return verifyDetailsIdentity(dir, expectedAccount, details, status)
}

func verifyDetailsIdentity(
	dir, expectedAccount string,
	details *AccountDetails,
	status string,
) (*AccountDetails, string) {
	if status != DetailsAvailable {
		return details, status
	}
	actual, err := accountIdentity(dir)
	if err != nil {
		return nil, DetailsUnavailable
	}
	if actual != expectedAccount {
		return nil, DetailsMismatch
	}
	return details, status
}
