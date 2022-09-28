package main

import (
	"flag"
	"os"

	"github.com/emiraganov/sipgo"
	"github.com/emiraganov/sipgo/sip"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

func main() {
	extIP := flag.String("ip", "127.0.0.1:5060", "My exernal ip")
	dst := flag.String("dst", "127.0.0.2:5060", "Destination pbx, sip server")
	flag.Parse()

	log.Logger = zerolog.New(zerolog.ConsoleWriter{
		Out:        os.Stdout,
		TimeFormat: "2006-01-02 15:04:05.000",
	}).With().Timestamp().Logger().Level(zerolog.InfoLevel)

	srv, err := sipgo.NewServerDialog()
	if err != nil {
		log.Fatal().Err(err).Msg("Fail to setup dialog server")
	}

	h := &Handler{
		srv,
		*dst,
	}

	setupRoutes(srv, h)

	log.Info().Str("ip", *extIP).Str("dst", *dst).Msg("Starting server")
	srv.Listen("udp", *extIP)
	if err := srv.Serve(); err != nil {
		log.Error().Err(err).Msg("Fail to serve")
	}
}

func setupRoutes(srv *sipgo.ServerDialog, h *Handler) {
	srv.OnInvite(h.route)
	srv.OnAck(h.route)
	srv.OnCancel(h.route)
	srv.OnBye(h.route)

	srv.OnDialog(h.onDialog)
}

type Handler struct {
	s   *sipgo.ServerDialog
	dst string
}

func (s *Handler) proxyDestination() string {
	return s.dst
}

// onDialog is our main function for handling dialogs
func (h *Handler) onDialog(d sip.Dialog) {
	log.Info().Str("state", d.StateString()).Str("ID", d.ID).Msg("New dialog <--")
	switch d.State {
	case sip.DialogStateEstablished:
		// 200 response
	case sip.DialogStateConfirmed:
		// ACK send
	case sip.DialogStateEnded:
		// BYE send
	}
}

// route is our main route function to proxy to our dst
func (h *Handler) route(req *sip.Request, tx sip.ServerTransaction) {
	dst := h.proxyDestination()
	req.SetDestination(dst)
	// Handle 200 Ack
	if req.IsAck() {
		if err := h.s.WriteRequest(req); err != nil {
			log.Error().Err(err).Msg("Send failed")
			reply(tx, req, 500, "")
			return
		}
		return
	}

	// Start client transaction and relay our request
	clTx, err := h.s.TransactionRequest(req)
	if err != nil {
		log.Error().Err(err).Msg("RequestWithContext  failed")
		reply(tx, req, 500, "")
		return
	}
	defer clTx.Terminate()

	for {
		select {
		case res, more := <-clTx.Responses():
			if !more {
				return
			}
			res.SetDestination(req.Source())
			if err := h.s.TransactionReply(tx, res); err != nil {
				log.Error().Err(err).Msg("ResponseHandler transaction respond failed")
			}

		case m := <-tx.Cancels():
			// Send response imediatelly
			reply(tx, m, 200, "OK")
			// Cancel client transacaction without waiting. This will send CANCEL request
			clTx.Cancel()

		case err := <-clTx.Errors():
			log.Error().Err(err).Str("caller", req.Method().String()).Msg("Client Transaction Error")
			return

		case err := <-tx.Errors():
			log.Error().Err(err).Str("caller", req.Method().String()).Msg("Server transaction error")
			return

		case <-tx.Done():
			log.Debug().Str("req", req.Method().String()).Msg("Transaction done")
			return
		case <-clTx.Done():
			log.Debug().Str("req", req.Method().String()).Msg("Client Transaction done")
			return
		}
	}
}

func reply(tx sip.ServerTransaction, req *sip.Request, code sip.StatusCode, reason string) {
	resp := sip.NewResponseFromRequest(req, code, reason, nil)
	resp.SetDestination(req.Source()) //This is optional, but can make sure not wrong via is read
	if err := tx.Respond(resp); err != nil {
		log.Error().Err(err).Msg("Fail to respond on transaction")
	}
}
