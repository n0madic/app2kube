name: Release
on:
  create:
    tags:
      - v*

jobs:
  release:
    name: Release on GitHub
    runs-on: ubuntu-latest
    steps:
      - name: Check out code
        uses: actions/checkout@v1

      - name: Create release on GitHub
        uses: docker://goreleaser/goreleaser:latest
        with:
          args: release
        env:
          GITHUB_TOKEN: ${{secrets.GITHUB_TOKEN}}
