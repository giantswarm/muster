# muster completion fish

Generate the autocompletion script for fish

## Synopsis

Generate the autocompletion script for the fish shell.

To load completions in your current shell session:

	muster completion fish | source

To load completions for every new session, execute once:

	muster completion fish > ~/.config/fish/completions/muster.fish

You will need to start a new shell for this setup to take effect.


```
muster completion fish [flags]
```

## Options

```
  -h, --help              help for fish
      --no-descriptions   disable completion descriptions
```

## SEE ALSO

* [muster completion](completion.md)	 - Generate the autocompletion script for the specified shell
