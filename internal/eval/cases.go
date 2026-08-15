package eval

// GoldenSet es el corpus de evaluación del agente. Cada caso nació de una
// verificación manual end-to-end (demo + HTTP) y se fija acá para medir
// regresiones de comportamiento con el LLM real.
//
// El principio: las respuestas deben basarse en datos REALES de las tools y
// en los DOCUMENTOS indexados — nunca en memoria del modelo. Los casos
// negativos (MustNotContain) cazan alucinaciones: números o campañas que no
// existen en la DB no deben aparecer.
var GoldenSet = []Case{
	{
		ID:            "retrasos",
		Description:   "detectar_retrasos: lotes con retraso en aplicaciones planificadas",
		Question:      "¿Hay lotes con retraso en las aplicaciones planificadas?",
		ExpectedTools: []string{"detectar_retrasos"},
		MustContain:   []string{"retraso", "lote"},
	},
	{
		ID:            "resumen-30dias",
		Description:   "resumir_aplicaciones: resumen de aplicaciones de los últimos 30 días",
		Question:      "Resumí las aplicaciones de los últimos 30 días",
		ExpectedTools: []string{"resumir_aplicaciones"},
		MustContain:   []string{"aplicacion"},
	},
	{
		ID:            "rendimiento-lote12",
		Description:   "consultar_rendimientos: rendimiento del lote 12 (3.10 tn/ha vs objetivo 4.50)",
		Question:      "¿Qué rindió el lote 12 en la campaña 2024/2025?",
		ExpectedTools: []string{"consultar_rendimientos"},
		MustContain:   []string{"lote 12", "3.10"},
	},
	{
		ID:            "protocolo-herbicidas",
		Description:   "RAG: protocolo de herbicidas en trigo (debe salir del documento, no de memoria)",
		Question:      "¿Cuál es el protocolo recomendado para aplicar herbicidas en trigo?",
		ExpectedTools: []string{"buscar_documentos"},
		MustContain:   []string{"glifosato", "2,4-D"},
		// 2,4-D amina es 1 L/ha; metsulfurón 6 g/ha. Si el modelo inventa
		// dosis, lo caza la subcadena prohibida:
		MustNotContain: []string{"3 L/ha de 2,4-D", "2,4-D a 2 L", "metsulfurón a 15"},
	},
	{
		ID:            "no-alucina-campana",
		Description:   "anti-alucinación: el modelo NO debe inventar datos de la campaña 2023/2024",
		Question:      "¿Qué rindió la soja en la campaña 2023/2024?",
		ExpectedTools: []string{"consultar_rendimientos"},
		MustContainAny: []string{
			"no tengo datos", "no tengo informacion", "no hay datos",
			"no encuentro", "no está disponible", "no esta disponible",
			"sin datos", "no registrada", "no existe",
		},
		MustNotContain: []string{"3,8", "3.8", "2.7", "2,7", "8.7", "8,7"},
	},
	{
		ID:            "aprobar-solicitud",
		Description:   "consultar_aprobaciones: el agente conoce el estado de las solicitudes HITL",
		Question:      "¿Qué solicitudes de aplicación hay pendientes de aprobación?",
		ExpectedTools: []string{"consultar_aprobaciones"},
		MustContain:   []string{"pendiente"},
	},
	{
		ID:          "programar-hitl",
		Description: "HITL: programar una aplicación crea una solicitud (NO inserta directo). Writes=true: se saltea por defecto",
		Question:    "Programá una aplicación de glifosato 3 L/ha en el lote 4 para la campaña 2026/2027",
		ExpectedTools: []string{
			"programar_aplicacion",
		},
		MustContain: []string{"pendiente", "aprob"},
		Writes:      true,
	},
	{
		ID:            "discernimiento-datos-no-rag",
		Description:   "discernimiento: el rendimiento vive en la DB, NO debe disparar el RAG",
		Question:      "¿Qué rindió el lote 12 en la campaña 2024/2025?",
		ExpectedTools: []string{"consultar_rendimientos"},
		ForbiddenTools: []string{
			"buscar_documentos", // el rendimiento está en la DB, no en documentos
		},
		MustContain: []string{"3.10"},
	},
	{
		ID:            "discernimiento-docs-no-datos",
		Description:   "discernimiento: el protocolo vive en documentos, NO debe disparar tools de datos",
		Question:      "¿Qué dosis de glifosato recomienda el manual para trigo?",
		ExpectedTools: []string{"buscar_documentos"},
		ForbiddenTools: []string{
			"consultar_rendimientos", // el protocolo vive en documentos, no en la DB
			"consultar_aplicaciones",
			"consultar_lotes",
		},
		MustContain: []string{"glifosato"},
	},
	{
		ID:              "discernimiento-hibrido",
		Description:     "discernimiento: caso híbrido, debe usar AMBAS tools (datos + documentos), cualquier orden",
		Question:        "¿Qué rindió el lote 12 y qué recomienda el manual de buenas prácticas para mejorar el rinde?",
		RequiredToolsAny: []string{"consultar_rendimientos", "buscar_documentos"},
	},
}