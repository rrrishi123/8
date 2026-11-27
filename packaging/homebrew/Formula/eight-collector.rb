# typed: false
# frozen_string_literal: true

# EightCollector — the 8 witness/collector of the four-system, as a Homebrew
# formula (#9793/#53 P3-BREW checkpoint). Builds the collector (a Go module at
# collector/ inside the repo) from source.
#
# INWARD checkpoint only: this formula + the tap layout are staged for review.
# Publishing a public `rrrishi123/homebrew-tap` repo is the one OUTWARD step and
# stays Rishi's to cut. Until the v0.0.5 tag is minted, install the tip:
#   brew install --HEAD rrrishi123/tap/eight-collector
class EightCollector < Formula
  desc "Witnessed-wire collector for the four-system (the 8 witness)"
  homepage "https://github.com/rrrishi123/8"
  license "MIT"
  head "https://github.com/rrrishi123/8.git", branch: "main"

  # STABLE RELEASE — fill in at the v0.0.5 cut (the tag is Rishi's to mint; the
  # collector is a nested module, so this builds the repo tarball, not `go install`):
  #   url "https://github.com/rrrishi123/8/archive/refs/tags/v0.0.5.tar.gz"
  #   sha256 "FILL_AT_CUT" # sha256 of the tag tarball
  # A git tag+revision stanza also works once the tag exists.

  depends_on "go" => :build

  def install
    cd "collector" do
      system "go", "build", *std_go_args(output: bin/"eight-collector")
    end
  end

  test do
    # The collector has no `version` subcommand yet, so prove the binary built and
    # its flags are wired: an unknown flag prints the usage banner and exits 2.
    # Assert -listen, present on EVERY version: head builds branch main, where
    # -allocator does not exist pre-cut (#9839 — the conditional #35 merged past).
    output = shell_output("#{bin}/eight-collector --badflag 2>&1", 2)
    assert_match "Usage of", output
    assert_match "-listen", output
  end
end
