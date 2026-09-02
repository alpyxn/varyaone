package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/alpyxn/varyaone/internal/products"
)

func TestDecodeVariantDefinitionPayloadAcceptsFrontendContract(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/api/v1/variant-definitions", strings.NewReader(`{
		"code":"RENK",
		"name":"Renk",
		"description":"Ürünün renk seçenekleri",
		"options":[{"code":"KIRMIZI","name":"Kırmızı","short_code":"KRM","sort_order":1}]
	}`))

	var input products.VariantDefinitionInput
	if err := decodeJSON(request, &input); err != nil {
		t.Fatalf("frontend varyant tanımı payload'ı reddedildi: %v", err)
	}
	if input.Code != "RENK" || input.Name != "Renk" || input.Description != "Ürünün renk seçenekleri" {
		t.Fatalf("varyant tanımı alanları yanlış çözümlendi: %+v", input)
	}
	if len(input.Options) != 1 || input.Options[0].Code != "KIRMIZI" {
		t.Fatalf("varyant seçenekleri yanlış çözümlendi: %+v", input.Options)
	}
}

func TestDecodeVariantDefinitionPayloadStillRejectsUnknownFields(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/api/v1/variant-definitions", strings.NewReader(`{
		"code":"RENK",
		"name":"Renk",
		"unexpected":"alan"
	}`))

	var input products.VariantDefinitionInput
	if err := decodeJSON(request, &input); err == nil {
		t.Fatal("tanımsız varyant tanımı alanı kabul edildi")
	}
}
