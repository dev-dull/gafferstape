package client

import (
	"encoding/json"
	"fmt"
	"time"
)

// Property is one entry from /api/property/get-all.
type Property struct {
	ID                                   string   `json:"id"`
	StreetAddress                        string   `json:"streetAddress"`
	City                                 string   `json:"city"`
	State                                string   `json:"state"`
	ZipCode                              string   `json:"zipCode"`
	TimeZone                             string   `json:"timeZone"`
	UTCOffsetInMinutes                   *int     `json:"utcOffsetInMinutes"`
	AerialImagePublicURL                 *string  `json:"aerialImagePublicUrl"`
	IsReadyForEnergyProductionMonitoring bool     `json:"isReadyForEnergyProductionMonitoring"`
	CanResumeRegistrationProcess         bool     `json:"canResumeRegistrationProcess"`
	IsSDDCRegistrationInProgress         bool     `json:"isSddcRegistrationInProgress"`
	ApplianceIDs                         []string `json:"applianceIds"`
}

// HomeOwner is the user portion of /api/auth/get-account-info.
type HomeOwner struct {
	ID                        string  `json:"id"`
	FirstName                 string  `json:"firstName"`
	LastName                  string  `json:"lastName"`
	Email                     string  `json:"email"`
	PhoneNumber               string  `json:"phoneNumber"`
	ActivatedDate             string  `json:"activatedDate"`
	IsVerificationLinkExpired bool    `json:"isVerificationLinkExpired"`
	IsEmailVerified           bool    `json:"isEmailVerified"`
	AssistingAdminName        *string `json:"assistingAdminName"`
	AssistingAdminID          *string `json:"assistingAdminId"`
	AuthenticationType        int     `json:"authenticationType"`
}

// Inverter is one physical inverter on a property.
type Inverter struct {
	ID           string `json:"id"`
	Manufacturer string `json:"manufacturer"`
	ModelNumber  string `json:"modelNumber"`
	SerialNumber string `json:"serialNumber"`
	IsActive     bool   `json:"isActive"`
}

// PropertyWithInverters is /api/auth/get-account-info's propertyInfo:
// the Property fields plus inverter hardware metadata.
type PropertyWithInverters struct {
	Property
	SystemSize                *float64          `json:"systemSize"`
	Inverters                 []Inverter        `json:"inverters"`
	InverterRegistrationCases []json.RawMessage `json:"inverterRegistrationCases"`
}

// Account is the full /api/auth/get-account-info response.
type Account struct {
	HomeOwner HomeOwner             `json:"homeOwnerInfo"`
	Property  PropertyWithInverters `json:"propertyInfo"`
}

// Production is one /api/energy/get-production response. Timestamps in
// SystemEnergy are property-local (no offset, no Z); use EnergySample.Time
// with the Location helper to parse them correctly.
type Production struct {
	StartTimestamp string         `json:"startTimestamp"`
	EndTimestamp   string         `json:"endTimestamp"`
	TimePeriod     string         `json:"timePeriod"`
	Interval       string         `json:"interval"`
	Unit           string         `json:"unit"`
	Timezone       string         `json:"timezone"`
	SystemEnergy   []EnergySample `json:"systemEnergy"`
}

// EnergySample is one production bucket: energy in kWh during the bucket
// whose start time is Date (in Production.Timezone).
type EnergySample struct {
	Date  string  `json:"date"`
	Value float64 `json:"value"`
}

// sampleTimestampFormat matches the timezone-naive timestamps the
// portal returns inside Production responses, e.g. "2026-05-14T06:00:00".
const sampleTimestampFormat = "2006-01-02T15:04:05"

// Location returns the time.Location named by Production.Timezone.
func (p Production) Location() (*time.Location, error) {
	return time.LoadLocation(p.Timezone)
}

// Time parses Date in loc.
func (s EnergySample) Time(loc *time.Location) (time.Time, error) {
	if loc == nil {
		return time.Time{}, fmt.Errorf("Time: nil location")
	}
	return time.ParseInLocation(sampleTimestampFormat, s.Date, loc)
}
