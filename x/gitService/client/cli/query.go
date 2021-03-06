package cli

import (
	encJson "encoding/json"
	"fmt"

	"github.com/cosmos/cosmos-sdk/client/context"
	"github.com/cosmos/cosmos-sdk/codec"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

// GetCmdListRefs returns Cobra command for listing Git references
func GetCmdListRefs(moduleName string, cdc *codec.Codec) *cobra.Command {
	return &cobra.Command{
		Use:   "list URI",
		Short: "List Git references in repository",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			uri := args[0]
			log.Debug().Msgf("Listing references of repo %v", uri)

			cliCtx := context.NewCLIContext().WithCodec(cdc)
			res, err := cliCtx.QueryWithData(fmt.Sprintf("custom/%s/listRefs/%s", moduleName, uri), nil)
			if err != nil {
				return err
			}

			var refs []string
			if err := encJson.Unmarshal(res, &refs); err != nil {
				return err
			}

			log.Debug().Msgf("Received refs: %v", refs)
			fmt.Printf("\n")

			return nil
		},
	}
}
