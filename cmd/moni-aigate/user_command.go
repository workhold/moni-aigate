package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/moni-ai/moni-aigate/internal/identity"
)

const userUsage = `usage:
  moni-aigate user list
  moni-aigate user set-password --email EMAIL --password-stdin
  moni-aigate user reset-mfa    --email EMAIL
  moni-aigate user suspend      --email EMAIL
  moni-aigate user activate     --email EMAIL

Host-side account recovery for when the console cannot help (lost password or
authenticator, mail recovery disabled). Every change ends all of the user's
sessions and is written to the audit log as operator-cli. It needs the same
database credential as the application; passwords are read from stdin only.`

type userAccounts interface {
	OperatorListUsers(context.Context) ([]identity.OperatorUser, error)
	OperatorSetPassword(context.Context, string, string) error
	OperatorResetMFA(context.Context, string) error
	OperatorSetStatus(context.Context, string, bool) error
}

func runUserCommand(ctx context.Context, store userAccounts, args []string, stdin io.Reader, stdout io.Writer) error {
	if len(args) == 0 {
		return errors.New(userUsage)
	}
	action, rest := args[0], args[1:]
	flags := flag.NewFlagSet("user "+action, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	email := flags.String("email", "", "account email")
	passwordStdin := flags.Bool("password-stdin", false, "read the new password from stdin")
	if err := flags.Parse(rest); err != nil || flags.NArg() != 0 {
		return errors.New(userUsage)
	}
	if action == "list" {
		if *email != "" || *passwordStdin {
			return errors.New(userUsage)
		}
		users, err := store.OperatorListUsers(ctx)
		if err != nil {
			return fmt.Errorf("user list failed")
		}
		for _, u := range users {
			fmt.Fprintf(stdout, "%s\t%s\tplatform_role=%s\tstatus=%s\tmfa=%t\n", u.ID, u.Email, u.PlatformRole, u.Status, u.MFAEnabled)
		}
		return nil
	}
	if *email == "" {
		return errors.New(userUsage)
	}
	var err error
	switch action {
	case "set-password":
		if !*passwordStdin {
			return errors.New(userUsage)
		}
		password, readErr := bufio.NewReader(stdin).ReadString('\n')
		if readErr != nil && len(password) == 0 {
			return errors.New("unable to read password")
		}
		err = store.OperatorSetPassword(ctx, *email, strings.TrimRight(password, "\r\n"))
	case "reset-mfa":
		err = store.OperatorResetMFA(ctx, *email)
	case "suspend":
		err = store.OperatorSetStatus(ctx, *email, true)
	case "activate":
		err = store.OperatorSetStatus(ctx, *email, false)
	default:
		return errors.New(userUsage)
	}
	switch {
	case err == nil:
		fmt.Fprintf(stdout, "%s done; all sessions of the account were ended\n", action)
		return nil
	case errors.Is(err, identity.ErrNotFound):
		return errors.New("user not found")
	case errors.Is(err, identity.ErrLastAdmin):
		return errors.New("refused: this is the last active administrator")
	case errors.Is(err, identity.ErrInvalid):
		return errors.New("invalid input: password must be 12-72 characters and the email must be valid")
	default:
		return fmt.Errorf("%s failed", action)
	}
}
