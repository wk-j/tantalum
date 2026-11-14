# Development

```sh
go test ./...
```

Building needs the Xcode command line tools: cgo links LocalAuthentication for Touch ID and AppKit for the
clipboard. Decisions are ADRs in [adr/](adr/). The web UI is plain ES modules in `cmd/ta/web/`, embedded in
the binary with no build step; the header of `cmd/ta/web/app.js` maps its `lib/`, `components/` and
`features/`.

The agent skill's guide is `cmd/ta/skill/guide.md`, embedded in the binary and printed by `ta skill show`;
edit the rules there. `cmd/ta/skill/stub.md` is the `SKILL.md` that `ta skill setup` installs, and
`skills/tantalum/SKILL.md` is a copy of it for tools that install skills from a repository; a test fails
when the two differ.

## Release to Homebrew

The formula is `Formula/tantalum.rb` in [wk-j/homebrew-tap](https://github.com/wk-j/homebrew-tap). It builds
`ta` from the source tarball GitHub serves for a tag and installs `skills/` into the formula's share
directory, so a release is a tag here and a version bump there. For version 0.2.0, for example:

1. Tag a commit on `main` and push the tag:

   ```sh
   git tag v0.2.0
   git push origin v0.2.0
   ```

2. Hash the tarball of that tag:

   ```sh
   curl -sL https://github.com/wk-j/tantalum/archive/refs/tags/v0.2.0.tar.gz | shasum -a 256
   ```

3. In the tap, set `url` to that tarball and `sha256` to the hash in `Formula/tantalum.rb`, commit as
   `Update tantalum to 0.2.0` and push.

4. Check the published formula:

   ```sh
   brew update
   brew upgrade tantalum        # or: brew install wk-j/tap/tantalum
   brew test tantalum
   ```

`brew install --HEAD wk-j/tap/tantalum` builds `main` instead of the latest tag.
