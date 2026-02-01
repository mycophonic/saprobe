NAME := saprobe
ALLOWED_LICENSES := Apache-2.0,BSD-2-Clause,BSD-3-Clause,MIT,Unlicense
LICENSE_IGNORES := --ignore gotest.tools

include hack/common.mk

##########################
# Apple ALAC reference (Apache-2.0)
##########################

ALAC_COMMIT := c38887c5c5e64a4b31108733bd79ca9b2496d987
ALAC_BUILD_DIR := bin/tmp/alac

# Build alacconvert from Apple's open-source ALAC reference implementation.
alacconvert: bin/alacconvert ## Build Apple ALAC reference converter

bin/alacconvert:
	@echo "=== Fetching Apple ALAC ($(ALAC_COMMIT)) ==="
	@rm -rf $(ALAC_BUILD_DIR)
	@mkdir -p bin
	@git clone --depth 1 \
		https://github.com/macosforge/alac.git \
		$(ALAC_BUILD_DIR)
	@echo "=== Building alacconvert ==="
	@cd $(ALAC_BUILD_DIR)/convert-utility && $(MAKE)
	@cp $(ALAC_BUILD_DIR)/convert-utility/alacconvert bin/alacconvert
	@echo "=== alacconvert built: bin/alacconvert ==="

clean-alacconvert: ## Clean ALAC build artifacts
	@rm -rf $(ALAC_BUILD_DIR) bin/alacconvert
