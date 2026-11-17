package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"reflect"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/spf13/cobra"

	"github.com/scolastico-dev/one-man-office/internal/aicmd"
	"github.com/scolastico-dev/one-man-office/internal/apiclient"
)

// ticketNumber parses "12" or "#12".
func ticketNumber(s string) (int, error) {
	n, err := strconv.Atoi(strings.TrimPrefix(strings.TrimSpace(s), "#"))
	if err != nil || n < 1 {
		return 0, fmt.Errorf("%q is not a ticket number (use for example 12 or #12)", s)
	}
	return n, nil
}

// stdinMark is the argument that stands for standard input: as a file
// (--body-file -) or as the only word of a text (omo ai comment 12 -).
const stdinMark = "-"

// readBody returns the text from a file ("-" is standard input) or, when no
// file is given, from the joined words; a lone "-" as the only word reads
// standard input too. Giving both a file and words is an error; so is empty
// text unless allowEmpty, a text that is only "-" (a dash that was meant to
// read standard input), and text that is not valid UTF-8. The text is kept
// as it is except that line endings become \n and whitespace at the very end
// is removed. Its size is checked by the server, which refuses a request
// body over its limit with too_large.
func readBody(cmd *cobra.Command, fileFlag, file string, words []string, allowEmpty bool) (string, error) {
	if len(words) == 1 && words[0] == stdinMark {
		if file != "" {
			return "", fmt.Errorf("give the text either as words or with --%s, not both", fileFlag)
		}
		file, words = stdinMark, nil
	}
	var text string
	switch {
	case file != "" && len(words) > 0:
		return "", fmt.Errorf("give the text either as words or with --%s, not both", fileFlag)
	case file == stdinMark:
		raw, err := io.ReadAll(cmd.InOrStdin())
		if err != nil {
			return "", fmt.Errorf("read standard input: %w", err)
		}
		text = string(raw)
	case file != "":
		raw, err := os.ReadFile(file)
		if err != nil {
			return "", fmt.Errorf("read --%s: %w", fileFlag, errors.Unwrap(err))
		}
		text = string(raw)
	default:
		text = strings.Join(words, " ")
	}
	source := "the text"
	if file == stdinMark {
		source = "standard input"
	} else if file != "" {
		source = "--" + fileFlag + " " + file
	}
	return checkText(text, source, textHint(fileFlag, words != nil || file == ""), allowEmpty)
}

// textHint names the ways to pass a text: as words (when the command takes
// them), from a file, or from standard input.
func textHint(fileFlag string, takesWords bool) string {
	if takesWords {
		return fmt.Sprintf("give it as words, or on standard input with a lone - or --%s -", fileFlag)
	}
	return fmt.Sprintf("give it with --%s FILE, or on standard input with --%s -", fileFlag, fileFlag)
}

// checkText refuses text that is not UTF-8, a text that is only "-" and,
// unless allowEmpty, an empty text; it returns the text with \n line endings
// and without whitespace at the very end.
func checkText(text, source, hint string, allowEmpty bool) (string, error) {
	if !utf8.ValidString(text) {
		return "", fmt.Errorf("%s is not valid UTF-8 text", source)
	}
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	text = strings.TrimRightFunc(text, unicode.IsSpace)
	switch trimmed := strings.TrimSpace(text); {
	case trimmed == stdinMark:
		return "", fmt.Errorf("%s is only \"-\", not a text: %s", source, hint)
	case trimmed == "" && !allowEmpty:
		return "", fmt.Errorf("%s is empty: %s", source, hint)
	}
	return text, nil
}

// addAICommand registers the `omo ai` group, built from the definitions in
// internal/aicmd.
func addAICommand(root *cobra.Command) {
	groups := map[string]*cobra.Command{}
	for _, g := range aicmd.Groups() {
		cmd := &cobra.Command{
			Use:     g.Use,
			Short:   g.Short,
			Long:    g.Long,
			Example: g.Example,
			Args:    cobra.NoArgs,
			RunE:    func(cmd *cobra.Command, _ []string) error { return cmd.Help() },
		}
		key := strings.Join(g.Path, " ")
		if key == "" {
			cmd.PersistentFlags().Bool("json", false, "print the server's JSON answer instead of text")
			root.AddCommand(cmd)
		} else {
			groups[strings.Join(g.Path[:len(g.Path)-1], " ")].AddCommand(cmd)
		}
		groups[key] = cmd
	}
	for _, def := range aicmd.Commands() {
		groups[strings.Join(def.Path[:len(def.Path)-1], " ")].AddCommand(aiCommand(def))
	}
}

// aiCommand is the command line of def: its arguments and flags come from
// the tags of its input (aicmd.Fields).
func aiCommand(def aicmd.Command) *cobra.Command {
	fields, err := aicmd.Fields(def.Input)
	if err != nil {
		panic(err) // a definition error, caught by the guard tests
	}
	cmd := &cobra.Command{
		Use:     def.Use,
		Short:   def.Short,
		Long:    def.Long,
		Example: def.Example,
		Args:    argsRule(fields),
	}
	for _, f := range fields {
		if f.Flag == "" {
			continue
		}
		flags := cmd.Flags()
		switch elem(f.Type).Kind() {
		case reflect.String:
			flags.String(f.Flag, f.Default, f.Help)
		case reflect.Int:
			flags.Int(f.Flag, 0, f.Help)
		case reflect.Bool:
			flags.Bool(f.Flag, false, f.Help)
		case reflect.Slice:
			if elem(f.Type).Elem().Kind() == reflect.Int {
				flags.IntSlice(f.Flag, nil, f.Help)
			} else {
				flags.StringArray(f.Flag, nil, f.Help)
			}
		}
		if f.Required {
			_ = cmd.MarkFlagRequired(f.Flag)
		}
	}
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		// Connect first (never starting a server), then read the input.
		client, err := apiclient.FromEnvironment()
		if err != nil {
			return err
		}
		ctx := cmd.Context()
		if ctx == nil {
			ctx = context.Background()
		}
		in, err := aiInput(cmd, def, fields, args)
		if err != nil {
			return aicmd.Explain(err)
		}
		res, err := def.Run(ctx, client, in)
		if err != nil {
			return aicmd.Explain(err)
		}
		if asJSON, _ := cmd.Flags().GetBool("json"); asJSON {
			_, err = fmt.Fprintln(cmd.OutOrStdout(), strings.TrimRight(string(res.Value), "\n"))
			return err
		}
		_, err = io.WriteString(cmd.OutOrStdout(), res.Text)
		return err
	}
	return cmd
}

func elem(t reflect.Type) reflect.Type {
	if t.Kind() == reflect.Pointer {
		return t.Elem()
	}
	return t
}

// argsRule is cobra's argument check for the positional fields.
func argsRule(fields []aicmd.Field) cobra.PositionalArgs {
	required, optional, words := 0, 0, false
	for _, f := range fields {
		switch {
		case f.Arg < 0:
		case f.Words:
			words = true
		case f.Optional:
			optional++
		default:
			required++
		}
	}
	switch {
	case words && required == 0:
		return cobra.ArbitraryArgs
	case words:
		return cobra.MinimumNArgs(required)
	case optional > 0 && required == 0:
		return cobra.MaximumNArgs(optional)
	case optional > 0:
		return cobra.RangeArgs(required, required+optional)
	case required == 0:
		return cobra.NoArgs
	default:
		return cobra.ExactArgs(required)
	}
}

// aiInput reads the input of def from the arguments, the flags, files and
// standard input. The checks of the input that need no text run before any
// file or standard input is read.
func aiInput(cmd *cobra.Command, def aicmd.Command, fields []aicmd.Field, args []string) (any, error) {
	ptr := reflect.New(def.Input)
	in := ptr.Elem()
	flags := cmd.Flags()
	for _, f := range fields {
		v := in.Field(f.Index)
		switch {
		case f.Text:
		case f.Arg >= 0:
			if f.Arg >= len(args) {
				continue // an optional argument left out
			}
			if f.Type.Kind() == reflect.Int {
				n, err := ticketNumber(args[f.Arg])
				if err != nil {
					return nil, err
				}
				v.SetInt(int64(n))
			} else {
				v.SetString(args[f.Arg])
			}
		default:
			if f.Type.Kind() == reflect.Pointer && !flags.Changed(f.Flag) {
				continue
			}
			var got any
			switch elem(f.Type) {
			case reflect.TypeFor[string]():
				got, _ = flags.GetString(f.Flag)
			case reflect.TypeFor[int]():
				got, _ = flags.GetInt(f.Flag)
			case reflect.TypeFor[bool]():
				got, _ = flags.GetBool(f.Flag)
			case reflect.TypeFor[[]string]():
				got, _ = flags.GetStringArray(f.Flag)
			case reflect.TypeFor[[]int]():
				got, _ = flags.GetIntSlice(f.Flag)
			}
			set(v, reflect.ValueOf(got))
		}
	}
	// Texts: which are given, then the checks, then the reading.
	type text struct {
		f     aicmd.Field
		file  string
		words []string
	}
	var texts []text
	for _, f := range fields {
		if !f.Text {
			continue
		}
		t := text{f: f}
		t.file, _ = flags.GetString(f.Flag)
		given := t.file != ""
		if f.Words {
			t.words = []string{}
			if f.Arg < len(args) {
				t.words = args[f.Arg:]
			}
			given = len(t.words) > 0 || flags.Changed(f.Flag) || f.Type.Kind() != reflect.Pointer
		}
		if !given {
			continue
		}
		set(in.Field(f.Index), reflect.ValueOf(""))
		texts = append(texts, t)
	}
	if err := def.Check(ptr.Interface()); err != nil {
		return nil, err
	}
	for _, t := range texts {
		body, err := readBody(cmd, t.f.Flag, t.file, t.words, t.f.AllowEmpty)
		if err != nil {
			return nil, err
		}
		set(in.Field(t.f.Index), reflect.ValueOf(body))
	}
	for _, f := range fields {
		v := in.Field(f.Index)
		if f.StdinHint == "" || v.Kind() == reflect.Pointer && v.IsNil() || reflect.Indirect(v).String() != stdinMark {
			continue
		}
		raw, err := io.ReadAll(cmd.InOrStdin())
		if err != nil {
			return nil, fmt.Errorf("read standard input: %w", err)
		}
		value, err := checkText(string(raw), "standard input", f.StdinHint, false)
		if err != nil {
			return nil, err
		}
		set(v, reflect.ValueOf(value))
	}
	return ptr.Interface(), nil
}

// set stores x in v, through a new pointer when v is one.
func set(v, x reflect.Value) {
	if v.Kind() == reflect.Pointer {
		p := reflect.New(v.Type().Elem())
		p.Elem().Set(x)
		v.Set(p)
		return
	}
	v.Set(x)
}
