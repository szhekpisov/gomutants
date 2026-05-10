# Vendored web assets

`mutation-test-elements.js` is the UMD bundle of [`mutation-testing-elements`](https://github.com/stryker-mutator/mutation-testing-elements), the web component used by `WriteHTML` to render the Stryker v2 report inside a self-contained HTML page.

Pinned version: **3.7.3**

To refresh:

```sh
curl -sL https://unpkg.com/mutation-testing-elements@<version>/dist/mutation-test-elements.js \
  -o internal/report/assets/mutation-test-elements.js
```

Then update the version above and run `go test ./internal/report/...` to confirm the embedded HTML still parses round-trip.

The bundle is embedded into the gomutants binary via `go:embed` (`internal/report/html.go`); it is not loaded over the network at report-render time.
