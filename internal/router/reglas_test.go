package router

import (
	"context"
	"reflect"
	"testing"

	"github.com/agro-agent/agro-agent/internal/tools"
)

func TestReglasClasificador(t *testing.T) {
	tests := []struct {
		name     string
		consulta string
		want     []tools.Dominio
	}{
		{
			name:     "datos puro: rinde de un lote en una campaña",
			consulta: "¿Qué rindió el lote 12 en la campaña 2024/2025?",
			want:     []tools.Dominio{tools.DominioDatos},
		},
		{
			name:     "documentos puro: dosis del manual",
			consulta: "¿Qué dosis de glifosato recomienda el manual para trigo?",
			want:     []tools.Dominio{tools.DominioDocumentos},
		},
		{
			name:     "híbrido: datos + manual de buenas prácticas",
			consulta: "¿Qué rindió el lote 12 y qué recomienda el manual de buenas prácticas para mejorar el rinde?",
			want:     []tools.Dominio{tools.DominioDatos, tools.DominioDocumentos},
		},
		{
			name:     "indefinido: sin keywords → exponer todo",
			consulta: "hola, ¿cómo estás?",
			want:     []tools.Dominio{},
		},
		{
			name:     "indefinido: consulta vacía",
			consulta: "",
			want:     []tools.Dominio{},
		},
		{
			name:     "insensible a acentos: aplicación y producción",
			consulta: "¿Cuál fue la producción de la aplicación en la campaña?",
			want:     []tools.Dominio{tools.DominioDatos},
		},
		{
			name:     "insensible a mayúsculas",
			consulta: "APLICACIÓN de HERBICIDA en el LOTE 4",
			want:     []tools.Dominio{tools.DominioDatos, tools.DominioDocumentos},
		},
		{
			name:     "plurales: toneladas y lotes",
			consulta: "¿Cuántas toneladas rindieron los lotes?",
			want:     []tools.Dominio{tools.DominioDatos},
		},
		{
			name:     "keyword como substring no matchea: pilote no es lote",
			consulta: "¿cuánto pesa el pilote del puente?",
			want:     []tools.Dominio{},
		},
		{
			name:     "la ñ se conserva: campaña NO matchea campana (indefinido, sesgo no barrera)",
			consulta: "¿Cómo va la campaña?",
			want:     []tools.Dominio{},
		},
		{
			name:     "acento y ñ conviven: hectárea y toneladas sí clasifican como datos",
			consulta: "¿Cuántas toneladas por hectárea hubo en la campaña 2025?",
			want:     []tools.Dominio{tools.DominioDatos},
		},
	}

	c := NewReglasClasificador()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := c.Clasificar(context.Background(), tt.consulta)
			if err != nil {
				t.Fatalf("Clasificar: error inesperado: %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Clasificar(%q) = %v, esperado %v", tt.consulta, got, tt.want)
			}
		})
	}
}
