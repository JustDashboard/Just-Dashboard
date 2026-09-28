package api

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/Wayy01/Just-Dashboard/backend/internal/deploy"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/go-chi/chi/v5"
)

// Traffic alerts: rules over a deployment's request record, delivered on the
// channels deployments already announce themselves on. Reads sit with every
// other deployment read; writes need system.admin, as every other setting
// that can make the dashboard send something does.

type trafficAlertList struct {
	Alerts []deploy.TrafficAlert `json:"alerts"`
	// Kinds is the vocabulary, so the form offers exactly what the server
	// accepts rather than a copy of it.
	Kinds []string `json:"kinds"`
}

func (s *Server) handleTrafficAlertList(w http.ResponseWriter, r *http.Request) error {
	projectID, err := parseID(r)
	if err != nil {
		return err
	}
	alerts, err := s.modules.deployAutomation.ListTrafficAlerts(r.Context(), projectID)
	if err != nil {
		return httpx.Internal(err)
	}
	httpx.JSON(w, http.StatusOK, trafficAlertList{Alerts: alerts, Kinds: deploy.TrafficAlertKinds})
	return nil
}

func (s *Server) handleTrafficAlertCreate(w http.ResponseWriter, r *http.Request) error {
	projectID, err := parseID(r)
	if err != nil {
		return err
	}
	environmentID, err := s.deploymentEnvironment(r)
	if err != nil {
		return err
	}
	var in deploy.TrafficAlertWrite
	if err := httpx.DecodeJSON(r, &in); err != nil {
		return err
	}
	alert, err := s.modules.deployAutomation.CreateTrafficAlert(r.Context(), projectID, environmentID, in)
	if err != nil {
		return mapTrafficAlertError(err)
	}
	httpx.JSON(w, http.StatusCreated, alert)
	return nil
}

func (s *Server) handleTrafficAlertUpdate(w http.ResponseWriter, r *http.Request) error {
	projectID, alertID, err := trafficAlertIDs(r)
	if err != nil {
		return err
	}
	var in deploy.TrafficAlertWrite
	if err := httpx.DecodeJSON(r, &in); err != nil {
		return err
	}
	alert, err := s.modules.deployAutomation.UpdateTrafficAlert(r.Context(), projectID, alertID, in)
	if err != nil {
		return mapTrafficAlertError(err)
	}
	httpx.JSON(w, http.StatusOK, alert)
	return nil
}

func (s *Server) handleTrafficAlertDelete(w http.ResponseWriter, r *http.Request) error {
	projectID, alertID, err := trafficAlertIDs(r)
	if err != nil {
		return err
	}
	if err := s.modules.deployAutomation.DeleteTrafficAlert(r.Context(), projectID, alertID); err != nil {
		return mapTrafficAlertError(err)
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// handleTrafficAlertTest delivers the rule as if it had just fired, so the
// operator sees the message on their own phone before trusting it to wake
// them.
func (s *Server) handleTrafficAlertTest(w http.ResponseWriter, r *http.Request) error {
	projectID, alertID, err := trafficAlertIDs(r)
	if err != nil {
		return err
	}
	alerts, err := s.modules.deployAutomation.ListTrafficAlerts(r.Context(), projectID)
	if err != nil {
		return httpx.Internal(err)
	}
	var alert *deploy.TrafficAlert
	for i := range alerts {
		if alerts[i].ID == alertID {
			alert = &alerts[i]
		}
	}
	if alert == nil {
		return mapTrafficAlertError(deploy.ErrTrafficAlertNotFound)
	}
	if s.modules.trafficAlerts == nil {
		return httpx.BadRequest("traffic alerts are not running")
	}
	envelope := s.modules.trafficAlerts.TestEnvelope(r.Context(), *alert)
	channels := alert.Channels
	if len(channels) == 0 {
		all, err := s.modules.deployAutomation.ListNotificationChannels(r.Context())
		if err != nil {
			return httpx.Internal(err)
		}
		for _, channel := range all {
			if channel.Enabled {
				channels = append(channels, channel.ID)
			}
		}
	}
	delivered, failed := 0, 0
	for _, id := range channels {
		if err := s.modules.deployAutomation.DeliverNotification(r.Context(), nil, id, envelope); err != nil {
			failed++
		} else {
			delivered++
		}
	}
	httpx.JSON(w, http.StatusOK, map[string]int{"delivered": delivered, "failed": failed, "channels": len(channels)})
	return nil
}

func trafficAlertIDs(r *http.Request) (projectID, alertID int64, err error) {
	projectID, err = parseID(r)
	if err != nil {
		return 0, 0, err
	}
	alertID, err = strconv.ParseInt(chi.URLParam(r, "alert"), 10, 64)
	if err != nil || alertID <= 0 {
		return 0, 0, httpx.BadRequest("invalid alert id")
	}
	return projectID, alertID, nil
}

func mapTrafficAlertError(err error) error {
	switch {
	case errors.Is(err, deploy.ErrTrafficAlertNotFound):
		return httpx.Err(http.StatusNotFound, "not_found", "traffic alert not found")
	case errors.Is(err, deploy.ErrInvalidNotification):
		return httpx.BadRequest("%v", err)
	}
	return mapDeployError(err)
}
