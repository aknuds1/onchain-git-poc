package cli

import (
	"bytes"
	"context"
	encJson "encoding/json"
	"fmt"
	"io"
	"regexp"

	cosmosContext "github.com/cosmos/cosmos-sdk/client/context"
	"github.com/cosmos/cosmos-sdk/client/utils"
	sdk "github.com/cosmos/cosmos-sdk/types"
	authtxb "github.com/cosmos/cosmos-sdk/x/auth/client/txbuilder"
	"github.com/joystream/onchain-git-poc/x/gitService"
	"github.com/rs/zerolog/log"

	"gopkg.in/src-d/go-git.v4/plumbing"
	"gopkg.in/src-d/go-git.v4/plumbing/protocol/packp"
	"gopkg.in/src-d/go-git.v4/plumbing/transport"
)

type joystreamClient struct {
	ep         *transport.Endpoint
	txBldr     authtxb.TxBuilder
	cliCtx     cosmosContext.CLIContext
	author     sdk.AccAddress
	moduleName string
}

var reRepoURI = regexp.MustCompile("^[^/]+/[^/]+$")

func newJoystreamClient(uri string, cliCtx cosmosContext.CLIContext, txBldr authtxb.TxBuilder,
	author sdk.AccAddress, moduleName string) (*joystreamClient, error) {
	if !reRepoURI.MatchString(uri) {
		return nil, fmt.Errorf("Repo URI on invalid format: '%s'", uri)
	}

	url := fmt.Sprintf("joystream://blockchain/%s", uri)
	ep, err := transport.NewEndpoint(url)
	if err != nil {
		log.Debug().Msgf("Failed to create endpoint for URL '%s'", url)
		return nil, err
	}
	return &joystreamClient{
		ep:         ep,
		txBldr:     txBldr,
		cliCtx:     cliCtx,
		author:     author,
		moduleName: moduleName,
	}, nil
}

func (*joystreamClient) NewUploadPackSession(*transport.Endpoint, transport.AuthMethod) (
	transport.UploadPackSession, error) {
	log.Debug().Msgf("Joystream client creating UploadPackSession")
	return nil, nil
}

type rpSession struct {
	authMethod transport.AuthMethod
	endpoint   *transport.Endpoint
	advRefs    *packp.AdvRefs
	cmdStatus  map[plumbing.ReferenceName]error
	firstErr   error
	unpackErr  error
	client     *joystreamClient
}

func (c *joystreamClient) NewReceivePackSession(ep *transport.Endpoint,
	authMethod transport.AuthMethod) (transport.ReceivePackSession, error) {
	log.Debug().Msgf("Joystream client creating ReceivePackSession")

	sess := &rpSession{
		authMethod: authMethod,
		endpoint:   ep,
		cmdStatus:  map[plumbing.ReferenceName]error{},
		client:     c,
	}
	return sess, nil
}

func (s *rpSession) AdvertisedReferences() (*packp.AdvRefs, error) {
	log.Debug().Msgf("Joystream client getting advertised references")

	queryPath := fmt.Sprintf("custom/%s/advertisedReferences/%s", s.client.moduleName,
		s.client.ep.Path[1:])
	log.Debug().Msgf("Joystream client making query, path: '%s'", queryPath)
	res, err := s.client.cliCtx.QueryWithData(queryPath, nil)
	if err != nil {
		return nil, err
	}

	var advRefs *packp.AdvRefs
	if err := encJson.Unmarshal(res, &advRefs); err != nil {
		return nil, err
	}
	log.Debug().Msgf("Joystream client got advertised references from server: %+v",
		advRefs.References)

	return advRefs, nil
}

// ReceivePack receives a ReferenceUpdateRequest, with a packfile stream as its Packfile
// property. The request in turn gets encoded to a binary blob that gets sent to a Joystream
// server, to store on the blockchain.
func (s *rpSession) ReceivePack(ctx context.Context, req *packp.ReferenceUpdateRequest) (
	*packp.ReportStatus, error) {

	log.Debug().Msgf("Joystream client sending reference update request to endpoint")

	// TODO: Make references update atomic

	log.Debug().Msgf("Joystream client encoding packfile...")
	buf := bytes.NewBuffer(nil)
	if _, err := io.Copy(buf, req.Packfile); err != nil {
		log.Debug().Msgf("Joystream client failed to encode packfile: %s", err)
		req.Packfile.Close()
		return s.reportStatus(), err
	}
	if err := req.Packfile.Close(); err != nil {
		return s.reportStatus(), err
	}

	repoURI := s.endpoint.Path[1:]
	log.Debug().Msgf("Creating MsgUpdateReferences, repo URI: '%s'", s.endpoint.Path)
	msg, err := gitService.NewMsgUpdateReferences(repoURI, req, buf.Bytes(),
		s.client.author)
	if err != nil {
		log.Debug().Msgf("Joystream client failed to create MsgUpdateReferences: %s", err)
		return s.reportStatus(), err
	}
	log.Debug().Msgf(
		"Joystream client sending MsgUpdateReferences to server for repo '%s' with %d command(s)",
		msg.URI, len(msg.Commands))

	if err := utils.CompleteAndBroadcastTxCli(s.client.txBldr, s.client.cliCtx,
		[]sdk.Msg{msg}); err != nil {
		log.Debug().Msgf("Sending MsgUpdateReferences to node failed: %s", err)
		return s.reportStatus(), err
	}

	return s.reportStatus(), nil

	// Encode as blob to send to server
	// buf := bytes.NewBuffer(nil)
	// if err := req.Encode(buf); err != nil {
	// 	return nil, err
	// }

	// req := packp.NewReferenceUpdateRequest()
	// if err := req.Decode(buf); err != nil {
	// 	return fmt.Errorf("error decoding: %s", err)
	// }

	// reportStatus := packp.NewReportStatus()
	// reportStatus.CommandStatuses = []*packp.CommandStatus{}
	// reportStatus.UnpackStatus = "ok"
	// error := reportStatus.Error()
	// if error != nil {
	// 	log.Debug().Msgf("Error making report status: %s", error)
	// 	return nil, error
	// }

	// log.Debug().Msgf("Returning report status: %v", reportStatus)
	// return reportStatus, nil
}

func (s *rpSession) reportStatus() *packp.ReportStatus {
	rs := packp.NewReportStatus()
	rs.UnpackStatus = "ok"

	if s.unpackErr != nil {
		rs.UnpackStatus = s.unpackErr.Error()
	}

	if s.cmdStatus == nil {
		return rs
	}

	for ref, err := range s.cmdStatus {
		msg := "ok"
		if err != nil {
			msg = err.Error()
		}
		status := &packp.CommandStatus{
			ReferenceName: ref,
			Status:        msg,
		}
		rs.CommandStatuses = append(rs.CommandStatuses, status)
	}

	return rs
}

func (s *rpSession) setStatus(ref plumbing.ReferenceName, err error) {
	s.cmdStatus[ref] = err
	if s.firstErr == nil && err != nil {
		s.firstErr = err
	}
}

func (*rpSession) Close() error {
	return nil
}
