package router

import (
	"context"
	"strings"

	"github.com/agro-agent/agro-agent/internal/tools"
)

// ReglasClasificador clasifica por palabras clave normalizadas: minúsculas,
// sin diacríticos y matcheadas por palabra completa. Es determinista, barato
// (sin LLM) y explica su decisión por keyword. Sesgo no barrera: sin hits
// devuelve [] y el agente expone todas las tools.
type ReglasClasificador struct{}

// NewReglasClasificador arma el clasificador por reglas. Sin estado: el
// mismo valor sirve para todos los requests concurrentes.
func NewReglasClasificador() *ReglasClasificador { return &ReglasClasificador{} }

// Clasificar cuenta cuántas keywords de cada dominio aparecen en la consulta
// y devuelve los dominios a exponer. Nunca falla: ante consultas vacías o sin
// hits el resultado es [] (indefinido), que el agente interpreta como
// "exponer todo".
func (ReglasClasificador) Clasificar(_ context.Context, consulta string) ([]tools.Dominio, error) {
	norm := normalizar(consulta)
	tokens := tokenizar(norm)

	datos := 0
	for _, k := range palabrasDatos {
		if contienePalabra(norm, tokens, k) {
			datos++
		}
	}
	documentos := 0
	for _, k := range palabrasDocumentos {
		if contienePalabra(norm, tokens, k) {
			documentos++
		}
	}

	switch {
	case datos > 0 && documentos == 0:
		return []tools.Dominio{tools.DominioDatos}, nil
	case documentos > 0 && datos == 0:
		return []tools.Dominio{tools.DominioDocumentos}, nil
	case datos > 0 && documentos > 0:
		return []tools.Dominio{tools.DominioDatos, tools.DominioDocumentos}, nil
	default:
		return []tools.Dominio{}, nil
	}
}

// palabrasDatos: léxico de datos ESTRUCTURADOS que viven en la DB relacional
// (lotes, rendimientos, aplicaciones, solicitudes). El porqué de cada grupo:
//
//	lote/lotes, campana/campanas → filtros de consultar_lotes.
//	rendimiento(s), rindio/rinde, tonelada(s), hectarea(s), produccion,
//	productividad, cosecha, sembrar/sembrado → el rinde cosechado es un número
//	de la DB, NO vive en documentos.
//	aplicacion/aplicaciones, retraso(s), atraso(s) → programar/consultar
//	aplicaciones es un flujo de la DB; el manual solo describe CÓMO aplicar.
//	aprobacion(es), solicitud(es), pendiente(s) → el estado del HITL vive en la
//	DB (consultar_aprobaciones).
var palabrasDatos = []string{
	"lote", "lotes",
	"rendimiento", "rendimientos", "rindio", "rinde",
	"aplicacion", "aplicaciones",
	"campana", "campanas",
	"retraso", "retrasos", "atraso", "atrasos",
	"aprobacion", "aprobaciones",
	"solicitud", "solicitudes",
	"pendiente", "pendientes",
	"tonelada", "toneladas",
	"hectarea", "hectareas",
	"produccion", "productividad",
	"sembrar", "sembrado",
	"cosecha",
}

// palabrasDocumentos: léxico de información DOCUMENTAL (RAG). Apuntan a
// protocolos y manuales indexados, no a filas de la DB. El porqué de cada
// grupo:
//
//	protocolo, manual, procedimiento, guia, practica, buenas practicas → el
//	corpus técnico indexado (el manual de buenas prácticas).
//	recomendacion, recomendado, recomendada → qué RECOMIENDA el manual, no qué
//	pasó en la DB.
//	dosis, herbicida(s), plaguicida(s), fungicida(s), fertilizante(s),
//	fertilizacion → dosis y productos RECOMENDADOS; lo aplicado de verdad es
//	dato (consultar_aplicaciones/resumir_aplicaciones).
//	informe(s), documento(s) → el corpus de informes y documentos indexados.
//	tolerancia, intervalo, seguridad → parámetros de manejo que define el
//	manual, no campos de una tabla.
var palabrasDocumentos = []string{
	"protocolo", "manual",
	"procedimiento",
	"recomendacion", "recomendado", "recomendada",
	"guia",
	"practica", "buenas practicas",
	"dosis",
	"herbicida", "herbicidas",
	"plaguicida", "plaguicidas",
	"fungicida", "fungicidas",
	"fertilizante", "fertilizacion", "fertilizantes",
	"informe", "informes",
	"documento", "documentos",
	"tolerancia", "intervalo", "seguridad",
}

// normalizar prepara la consulta para el matcheo: minúsculas + sin
// diacríticos. Así "Aplicación", "APLICACION" y "aplicacion" caen en la misma
// keyword. La ñ se conserva: es una letra del alfabeto español, no un acento,
// y la keyword "campana" del léxico NO la sustituye ("campaña" con ñ no
// matchea). Es una limitación intencional del contrato: la clasificación de
// una consulta que solo menciona "campaña" queda indefinida y el agente
// expone todas las tools (sesgo no barrera), nunca un falso positivo.
var diacriticos = strings.NewReplacer(
	"á", "a", "à", "a", "â", "a", "ä", "a", "ã", "a",
	"é", "e", "è", "e", "ê", "e", "ë", "e",
	"í", "i", "ì", "i", "î", "i", "ï", "i",
	"ó", "o", "ò", "o", "ô", "o", "ö", "o", "õ", "o",
	"ú", "u", "ù", "u", "û", "u", "ü", "u",
)

func normalizar(s string) string {
	return diacriticos.Replace(strings.ToLower(s))
}

// tokenizar separa la consulta normalizada en palabras (secuencias de letras
// a-z + ñ). La palabra es la unidad de match: evita falsos positivos por
// substring (p. ej. "lote" dentro de "pilote", "dosis" dentro de otra
// palabra), que romperían la clasificación de dominio.
func tokenizar(s string) map[string]bool {
	out := make(map[string]bool)
	var w strings.Builder
	flush := func() {
		if w.Len() > 0 {
			out[w.String()] = true
			w.Reset()
		}
	}
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || r == 'ñ' {
			w.WriteRune(r)
		} else {
			flush()
		}
	}
	flush()
	return out
}

// contienePalabra matchea una keyword contra la consulta normalizada. Las
// keywords de una sola palabra se comparan contra el set de tokens; las de
// varias ("buenas practicas") contra la cadena normalizada, donde el espacio
// ya actúa de límite de palabra.
func contienePalabra(norm string, tokens map[string]bool, keyword string) bool {
	if strings.Contains(keyword, " ") {
		return strings.Contains(norm, keyword)
	}
	return tokens[keyword]
}
