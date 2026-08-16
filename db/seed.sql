-- =============================================================================
-- agro-agent · Seed v1 — Cooperativa "La Esperanza"
--
-- HISTORIAS DE DEMO montadas en los datos:
--  1. RETRASO: lotes 4, 7 y 12 tienen aplicaciones planificadas con fecha
--     vencida (relativa al momento de la siembra) → "¿Hay algún lote con
--     retraso?" responde SI.
--  2. COMPARACIÓN DE CAMPAÑAS: lote 12 rindió 3.1 tn/ha en 2024/2025 (helada
--     de julio 2024 pegó en lotes 7-9, 12) vs objetivo 4.5 en 2026/2027.
--  3. RESUMEN 30 DÍAS: aplicaciones ejecutadas dentro de los últimos 30 días.
--
-- TODAS las fechas son RELATIVAS a CURRENT_DATE (momento en que se ejecuta
-- este script, es decir la inicialización del volumen vía initdb). Ninguna
-- fecha es absoluta: así el demo no "envejece" y las ventanas (retrasos,
-- resumen 30 días, planificadas futuras) siguen teniendo sentido aunque pasen
-- meses entre una inicialización y otra. Este script SOLO corre en un volumen
-- vacío (docker-entrypoint-initdb.d), por lo que CURRENT_DATE se evalúa una
-- única vez por contenedor.
-- =============================================================================

BEGIN;

-- -----------------------------------------------------------------------------
-- Tenant
-- -----------------------------------------------------------------------------
INSERT INTO tenants (id, name, slug) VALUES
    (1, 'Cooperativa La Esperanza', 'la-esperanza');

-- -----------------------------------------------------------------------------
-- Usuarios
-- -----------------------------------------------------------------------------
INSERT INTO users (id, tenant_id, email, display_name, role) VALUES
    (1, 1, 'ana.torres@la-esperanza.coop',  'Ana Torres',   'admin'),
    (2, 1, 'carlos.gutierrez@la-esperanza.coop', 'Carlos Gutiérrez', 'agronomo'),
    (3, 1, 'marta.rios@la-esperanza.coop',  'Marta Ríos',   'agronomo'),
    (4, 1, 'pedro.sanchez@la-esperanza.coop', 'Pedro Sánchez', 'productor'),
    (5, 1, 'jorge.fernandez@la-esperanza.coop', 'Jorge Fernández', 'productor'),
    (6, 1, 'lucia.molina@la-esperanza.coop', 'Lucía Molina', 'productor');

-- -----------------------------------------------------------------------------
-- Lotes (Carlos responde lotes 1-8, Marta lotes 9-18)
-- -----------------------------------------------------------------------------
INSERT INTO lotes (id, tenant_id, codigo, nombre, superficie_ha, tipo_suelo, responsable_id) VALUES
    (1,  1, '1',  'El Rincón',      48.5, 'franco-arcilloso', 2),
    (2,  1, '2',  'La Loma',        62.0, 'franco',           2),
    (3,  1, '3',  'Cañada Sur',     55.2, 'franco-limosos',   2),
    (4,  1, '4',  'Bajo Grande',    70.1, 'arcilloso',        2),
    (5,  1, '5',  'La Perdiz',      41.8, 'franco',           2),
    (6,  1, '6',  'El Tala',        58.3, 'franco-arenoso',   2),
    (7,  1, '7',  'Media Luna',     66.0, 'franco',           2),
    (8,  1, '8',  'San Roque',      74.5, 'arcilloso',        2),
    (9,  1, '9',  'Los Tordos',     52.9, 'franco-limosos',   3),
    (10, 1, '10', 'Vega Norte',     60.4, 'franco',           3),
    (11, 1, '11', 'El Chañar',      45.0, 'franco-arenoso',   3),
    (12, 1, '12', 'La Esperanza II', 68.7, 'franco',          3),
    (13, 1, '13', 'Colonia Vieja',  39.6, 'franco-arcilloso', 3),
    (14, 1, '14', 'Piedra Blanca',  51.2, 'arcilloso',        3),
    (15, 1, '15', 'El Quebracho',   82.3, 'franco',           3),
    (16, 1, '16', 'La Blanqueada',  57.0, 'franco-limosos',   3),
    (17, 1, '17', 'Santa Clara',    63.8, 'franco',           3),
    (18, 1, '18', 'Los Algarrobos', 49.1, 'franco-arenoso',   3);

-- -----------------------------------------------------------------------------
-- Campañas
--   1: 2024/2025 fina (trigo)   — ya cosechada (histórica)
--   2: 2024/2025 gruesa (soja/maíz) — ya cosechada (histórica)
--   3: 2026/2027 fina (trigo)   — CAMPAÑA ACTUAL (en curso)
-- -----------------------------------------------------------------------------
INSERT INTO campanas (id, tenant_id, nombre, temporada, fecha_inicio, fecha_fin) VALUES
    -- Campañas históricas: siempre en el pasado, ancladas a ~2 años atrás.
    (1, 1, '2024/2025', 'fina',   CURRENT_DATE - INTERVAL '804 days', CURRENT_DATE - INTERVAL '602 days'),
    (2, 1, '2024/2025', 'gruesa', CURRENT_DATE - INTERVAL '682 days', CURRENT_DATE - INTERVAL '456 days'),
    -- Campaña actual: comienza hace ~2,5 meses y sigue abierta (sin fecha_fin).
    (3, 1, '2026/2027', 'fina',   CURRENT_DATE - INTERVAL '74 days',  NULL);

-- Lotes por campaña con cultivo y objetivo de rinde (tn/ha)
INSERT INTO campana_lotes (id, tenant_id, campana_id, lote_id, cultivo, rendimiento_objetivo) VALUES
    -- Campaña 1 (2024/2025 fina · trigo) — histórico
    (1,  1, 1, 1,  'trigo', 4.2), (2,  1, 1, 2,  'trigo', 4.0),
    (3,  1, 1, 3,  'trigo', 4.4), (4,  1, 1, 4,  'trigo', 4.1),
    (5,  1, 1, 5,  'trigo', 4.3), (6,  1, 1, 6,  'trigo', 4.5),
    (7,  1, 1, 7,  'trigo', 4.2), (8,  1, 1, 8,  'trigo', 4.0),
    (9,  1, 1, 9,  'trigo', 4.4), (10, 1, 1, 10, 'trigo', 4.1),
    (11, 1, 1, 11, 'trigo', 4.5), (12, 1, 1, 12, 'trigo', 4.2),
    -- Campaña 2 (2024/2025 gruesa) — histórico
    (13, 1, 2, 8,  'soja', 3.5), (14, 1, 2, 9,  'soja', 3.6),
    (15, 1, 2, 10, 'soja', 3.8), (16, 1, 2, 11, 'soja', 3.4),
    (17, 1, 2, 12, 'soja', 3.7), (18, 1, 2, 13, 'soja', 3.5),
    (19, 1, 2, 14, 'soja', 3.6), (20, 1, 2, 15, 'maiz', 9.0),
    (21, 1, 2, 16, 'maiz', 8.8), (22, 1, 2, 17, 'maiz', 9.2),
    (23, 1, 2, 18, 'maiz', 8.5),
    -- Campaña 3 (2026/2027 fina · trigo) — ACTUAL
    (24, 1, 3, 1,  'trigo', 4.5), (25, 1, 3, 2,  'trigo', 4.2),
    (26, 1, 3, 3,  'trigo', 4.6), (27, 1, 3, 4,  'trigo', 4.3),
    (28, 1, 3, 5,  'trigo', 4.5), (29, 1, 3, 6,  'trigo', 4.7),
    (30, 1, 3, 7,  'trigo', 4.4), (31, 1, 3, 8,  'trigo', 4.2),
    (32, 1, 3, 9,  'trigo', 4.6), (33, 1, 3, 10, 'trigo', 4.3),
    (34, 1, 3, 11, 'trigo', 4.7), (35, 1, 3, 12, 'trigo', 4.5);

-- -----------------------------------------------------------------------------
-- Productos (insumos)
-- -----------------------------------------------------------------------------
INSERT INTO productos (id, tenant_id, nombre, tipo, principio_activo, unidad) VALUES
    (1,  1, 'Glifosato 48%',        'herbicida',   'glifosato',          'L'),
    (2,  1, '2,4-D Amina',          'herbicida',   '2,4-D amina',        'L'),
    (3,  1, 'Metsulfurón 60%',      'herbicida',   'metsulfuron-metil',  'g'),
    (4,  1, 'Tebuconazol 25%',      'fungicida',   'tebuconazol',        'L'),
    (5,  1, 'Azoxistrobina 20%',    'fungicida',   'azoxistrobina',      'L'),
    (6,  1, 'Cipermetrina 25%',     'insecticida', 'cipermetrina',       'L'),
    (7,  1, 'Urea granulada 46%',   'fertilizante','nitrógeno',          'kg'),
    (8,  1, 'MAP 11-52',            'fertilizante','fósforo',            'kg'),
    (9,  1, 'Sulfato de amonio 21%','fertilizante','nitrógeno + azufre', 'kg'),
    (10, 1, 'Semilla trigo B620',   'semilla',     'Triticum aestivum',  'kg');

-- -----------------------------------------------------------------------------
-- Aplicaciones — campaña ACTUAL (3): 2026/2027 fina
--   Barbecho químico + fertilización de siembra: EJECUTADAS (hace ~2 meses)
--   Herbicida post-emergencia: la mayoría EJECUTADA (últimos ~8 días), pero
--     lotes 4, 7 y 12 siguen PLANIFICADAS con fecha vencida → RETRASO (demo #1)
--   Fungicida/insecticida: PLANIFICADAS a futuro (+1 a +2 meses)
-- -----------------------------------------------------------------------------
INSERT INTO aplicaciones
    (id, tenant_id, lote_id, campana_id, producto_id, estado,
     dosis, unidad_dosis, fecha_planificada, fecha_ejecucion, ejecutada_por_id, notas) VALUES
    -- Barbecho químico (glifosato) — ejecutado hace ~2 meses
    (1,  1, 1,  3, 1, 'ejecutada', 3.0, 'L/ha', CURRENT_DATE - INTERVAL '64 days', CURRENT_DATE - INTERVAL '64 days', 2, NULL),
    (2,  1, 2,  3, 1, 'ejecutada', 3.0, 'L/ha', CURRENT_DATE - INTERVAL '64 days', CURRENT_DATE - INTERVAL '64 days', 2, NULL),
    (3,  1, 3,  3, 1, 'ejecutada', 3.0, 'L/ha', CURRENT_DATE - INTERVAL '63 days', CURRENT_DATE - INTERVAL '63 days', 2, NULL),
    (4,  1, 4,  3, 1, 'ejecutada', 3.0, 'L/ha', CURRENT_DATE - INTERVAL '63 days', CURRENT_DATE - INTERVAL '62 days', 2, NULL),
    (5,  1, 5,  3, 1, 'ejecutada', 3.0, 'L/ha', CURRENT_DATE - INTERVAL '61 days', CURRENT_DATE - INTERVAL '61 days', 3, NULL),
    (6,  1, 6,  3, 1, 'ejecutada', 3.0, 'L/ha', CURRENT_DATE - INTERVAL '61 days', CURRENT_DATE - INTERVAL '60 days', 3, NULL),
    (7,  1, 7,  3, 1, 'ejecutada', 3.0, 'L/ha', CURRENT_DATE - INTERVAL '59 days', CURRENT_DATE - INTERVAL '59 days', 3, NULL),
    (8,  1, 8,  3, 1, 'ejecutada', 3.0, 'L/ha', CURRENT_DATE - INTERVAL '59 days', CURRENT_DATE - INTERVAL '58 days', 3, NULL),
    (9,  1, 9,  3, 1, 'ejecutada', 3.0, 'L/ha', CURRENT_DATE - INTERVAL '57 days', CURRENT_DATE - INTERVAL '57 days', 3, NULL),
    (10, 1, 10, 3, 1, 'ejecutada', 3.0, 'L/ha', CURRENT_DATE - INTERVAL '57 days', CURRENT_DATE - INTERVAL '56 days', 3, NULL),
    (11, 1, 11, 3, 1, 'ejecutada', 3.0, 'L/ha', CURRENT_DATE - INTERVAL '55 days', CURRENT_DATE - INTERVAL '55 days', 3, NULL),
    (12, 1, 12, 3, 1, 'ejecutada', 3.0, 'L/ha', CURRENT_DATE - INTERVAL '55 days', CURRENT_DATE - INTERVAL '54 days', 3, NULL),
    -- Fertilización de siembra (MAP + urea) — ejecutado hace ~1 mes
    (13, 1, 1,  3, 8, 'ejecutada', 80.0, 'kg/ha', CURRENT_DATE - INTERVAL '44 days', CURRENT_DATE - INTERVAL '43 days', 2, 'MAP a la siembra'),
    (14, 1, 2,  3, 8, 'ejecutada', 80.0, 'kg/ha', CURRENT_DATE - INTERVAL '44 days', CURRENT_DATE - INTERVAL '43 days', 2, NULL),
    (15, 1, 3,  3, 8, 'ejecutada', 80.0, 'kg/ha', CURRENT_DATE - INTERVAL '42 days', CURRENT_DATE - INTERVAL '42 days', 2, NULL),
    (16, 1, 4,  3, 8, 'ejecutada', 80.0, 'kg/ha', CURRENT_DATE - INTERVAL '42 days', CURRENT_DATE - INTERVAL '41 days', 2, NULL),
    (17, 1, 5,  3, 8, 'ejecutada', 80.0, 'kg/ha', CURRENT_DATE - INTERVAL '40 days', CURRENT_DATE - INTERVAL '40 days', 3, NULL),
    (18, 1, 6,  3, 8, 'ejecutada', 80.0, 'kg/ha', CURRENT_DATE - INTERVAL '40 days', CURRENT_DATE - INTERVAL '39 days', 3, NULL),
    (19, 1, 7,  3, 8, 'ejecutada', 80.0, 'kg/ha', CURRENT_DATE - INTERVAL '38 days', CURRENT_DATE - INTERVAL '38 days', 3, NULL),
    (20, 1, 8,  3, 8, 'ejecutada', 80.0, 'kg/ha', CURRENT_DATE - INTERVAL '38 days', CURRENT_DATE - INTERVAL '37 days', 3, NULL),
    (21, 1, 9,  3, 8, 'ejecutada', 80.0, 'kg/ha', CURRENT_DATE - INTERVAL '36 days', CURRENT_DATE - INTERVAL '36 days', 3, NULL),
    (22, 1, 10, 3, 8, 'ejecutada', 80.0, 'kg/ha', CURRENT_DATE - INTERVAL '36 days', CURRENT_DATE - INTERVAL '35 days', 3, NULL),
    (23, 1, 11, 3, 8, 'ejecutada', 80.0, 'kg/ha', CURRENT_DATE - INTERVAL '34 days', CURRENT_DATE - INTERVAL '34 days', 3, NULL),
    (24, 1, 12, 3, 8, 'ejecutada', 80.0, 'kg/ha', CURRENT_DATE - INTERVAL '34 days', CURRENT_DATE - INTERVAL '33 days', 3, NULL),
    (25, 1, 1,  3, 7, 'ejecutada', 120.0, 'kg/ha', CURRENT_DATE - INTERVAL '44 days', CURRENT_DATE - INTERVAL '43 days', 2, 'Urea a la siembra'),
    (26, 1, 2,  3, 7, 'ejecutada', 120.0, 'kg/ha', CURRENT_DATE - INTERVAL '44 days', CURRENT_DATE - INTERVAL '43 days', 2, NULL),
    (27, 1, 3,  3, 7, 'ejecutada', 120.0, 'kg/ha', CURRENT_DATE - INTERVAL '42 days', CURRENT_DATE - INTERVAL '42 days', 2, NULL),
    (28, 1, 4,  3, 7, 'ejecutada', 120.0, 'kg/ha', CURRENT_DATE - INTERVAL '42 days', CURRENT_DATE - INTERVAL '41 days', 2, NULL),
    (29, 1, 5,  3, 7, 'ejecutada', 120.0, 'kg/ha', CURRENT_DATE - INTERVAL '40 days', CURRENT_DATE - INTERVAL '40 days', 3, NULL),
    (30, 1, 6,  3, 7, 'ejecutada', 120.0, 'kg/ha', CURRENT_DATE - INTERVAL '40 days', CURRENT_DATE - INTERVAL '39 days', 3, NULL),
    (31, 1, 7,  3, 7, 'ejecutada', 120.0, 'kg/ha', CURRENT_DATE - INTERVAL '38 days', CURRENT_DATE - INTERVAL '38 days', 3, NULL),
    (32, 1, 8,  3, 7, 'ejecutada', 120.0, 'kg/ha', CURRENT_DATE - INTERVAL '38 days', CURRENT_DATE - INTERVAL '37 days', 3, NULL),
    (33, 1, 9,  3, 7, 'ejecutada', 120.0, 'kg/ha', CURRENT_DATE - INTERVAL '36 days', CURRENT_DATE - INTERVAL '36 days', 3, NULL),
    (34, 1, 10, 3, 7, 'ejecutada', 120.0, 'kg/ha', CURRENT_DATE - INTERVAL '36 days', CURRENT_DATE - INTERVAL '35 days', 3, NULL),
    (35, 1, 11, 3, 7, 'ejecutada', 120.0, 'kg/ha', CURRENT_DATE - INTERVAL '34 days', CURRENT_DATE - INTERVAL '34 days', 3, NULL),
    (36, 1, 12, 3, 7, 'ejecutada', 120.0, 'kg/ha', CURRENT_DATE - INTERVAL '34 days', CURRENT_DATE - INTERVAL '33 days', 3, NULL),
    -- Herbicida post-emergencia — la mayoría ejecutada, TRES con retraso
    (37, 1, 1,  3, 2, 'ejecutada', 1.0, 'L/ha', CURRENT_DATE - INTERVAL '9 days',  CURRENT_DATE - INTERVAL '8 days',  2, NULL),
    (38, 1, 2,  3, 2, 'ejecutada', 1.0, 'L/ha', CURRENT_DATE - INTERVAL '9 days',  CURRENT_DATE - INTERVAL '8 days',  2, NULL),
    (39, 1, 3,  3, 2, 'ejecutada', 1.0, 'L/ha', CURRENT_DATE - INTERVAL '7 days',  CURRENT_DATE - INTERVAL '7 days',  2, NULL),
    (40, 1, 4,  3, 2, 'planificada', 1.0, 'L/ha', CURRENT_DATE - INTERVAL '6 days', NULL, NULL, 'RETRASO: esperando condición de viento'),
    (41, 1, 5,  3, 2, 'ejecutada', 1.0, 'L/ha', CURRENT_DATE - INTERVAL '5 days',  CURRENT_DATE - INTERVAL '4 days',  3, NULL),
    (42, 1, 6,  3, 2, 'ejecutada', 1.0, 'L/ha', CURRENT_DATE - INTERVAL '5 days',  CURRENT_DATE - INTERVAL '4 days',  3, NULL),
    (43, 1, 7,  3, 2, 'planificada', 1.0, 'L/ha', CURRENT_DATE - INTERVAL '4 days', NULL, NULL, 'RETRASO: a la espera de autorización'),
    (44, 1, 8,  3, 2, 'ejecutada', 1.0, 'L/ha', CURRENT_DATE - INTERVAL '3 days',  CURRENT_DATE - INTERVAL '2 days',  3, NULL),
    (45, 1, 9,  3, 2, 'ejecutada', 1.0, 'L/ha', CURRENT_DATE - INTERVAL '3 days',  CURRENT_DATE - INTERVAL '2 days',  3, NULL),
    (46, 1, 10, 3, 2, 'ejecutada', 1.0, 'L/ha', CURRENT_DATE - INTERVAL '2 days',  CURRENT_DATE - INTERVAL '1 days',  3, NULL),
    (47, 1, 11, 3, 2, 'ejecutada', 1.0, 'L/ha', CURRENT_DATE - INTERVAL '2 days',  CURRENT_DATE - INTERVAL '1 days',  3, NULL),
    (48, 1, 12, 3, 2, 'planificada', 1.0, 'L/ha', CURRENT_DATE - INTERVAL '9 days', NULL, NULL, 'RETRASO: 9 días vencida'),
    -- Fungicida + insecticida — planificadas a futuro
    (49, 1, 1,  3, 4, 'planificada', 0.6, 'L/ha', CURRENT_DATE + INTERVAL '32 days', NULL, NULL, NULL),
    (50, 1, 2,  3, 4, 'planificada', 0.6, 'L/ha', CURRENT_DATE + INTERVAL '32 days', NULL, NULL, NULL),
    (51, 1, 3,  3, 4, 'planificada', 0.6, 'L/ha', CURRENT_DATE + INTERVAL '33 days', NULL, NULL, NULL),
    (52, 1, 4,  3, 4, 'planificada', 0.6, 'L/ha', CURRENT_DATE + INTERVAL '33 days', NULL, NULL, NULL),
    (53, 1, 5,  3, 4, 'planificada', 0.6, 'L/ha', CURRENT_DATE + INTERVAL '34 days', NULL, NULL, NULL),
    (54, 1, 6,  3, 4, 'planificada', 0.6, 'L/ha', CURRENT_DATE + INTERVAL '34 days', NULL, NULL, NULL),
    (55, 1, 1,  3, 6, 'planificada', 0.15, 'L/ha', CURRENT_DATE + INTERVAL '52 days', NULL, NULL, NULL),
    (56, 1, 4,  3, 6, 'planificada', 0.15, 'L/ha', CURRENT_DATE + INTERVAL '52 days', NULL, NULL, NULL),
    (57, 1, 9,  3, 6, 'planificada', 0.15, 'L/ha', CURRENT_DATE + INTERVAL '53 days', NULL, NULL, NULL),
    (58, 1, 12, 3, 6, 'planificada', 0.15, 'L/ha', CURRENT_DATE + INTERVAL '53 days', NULL, NULL, NULL);

-- -----------------------------------------------------------------------------
-- Aplicaciones — campañas históricas (ejecutadas hace ~2 años)
-- -----------------------------------------------------------------------------
INSERT INTO aplicaciones
    (id, tenant_id, lote_id, campana_id, producto_id, estado,
     dosis, unidad_dosis, fecha_planificada, fecha_ejecucion, ejecutada_por_id, notas) VALUES
    -- Campaña 1 (2024/2025 fina · trigo)
    (59, 1, 1,  1, 1, 'ejecutada', 3.0, 'L/ha', CURRENT_DATE - INTERVAL '790 days', CURRENT_DATE - INTERVAL '789 days', 2, NULL),
    (60, 1, 2,  1, 1, 'ejecutada', 3.0, 'L/ha', CURRENT_DATE - INTERVAL '790 days', CURRENT_DATE - INTERVAL '789 days', 2, NULL),
    (61, 1, 3,  1, 1, 'ejecutada', 3.0, 'L/ha', CURRENT_DATE - INTERVAL '788 days', CURRENT_DATE - INTERVAL '787 days', 2, NULL),
    (62, 1, 4,  1, 1, 'ejecutada', 3.0, 'L/ha', CURRENT_DATE - INTERVAL '788 days', CURRENT_DATE - INTERVAL '787 days', 2, NULL),
    (63, 1, 5,  1, 1, 'ejecutada', 3.0, 'L/ha', CURRENT_DATE - INTERVAL '786 days', CURRENT_DATE - INTERVAL '785 days', 3, NULL),
    (64, 1, 6,  1, 1, 'ejecutada', 3.0, 'L/ha', CURRENT_DATE - INTERVAL '786 days', CURRENT_DATE - INTERVAL '785 days', 3, NULL),
    (65, 1, 7,  1, 1, 'ejecutada', 3.0, 'L/ha', CURRENT_DATE - INTERVAL '784 days', CURRENT_DATE - INTERVAL '783 days', 3, NULL),
    (66, 1, 8,  1, 1, 'ejecutada', 3.0, 'L/ha', CURRENT_DATE - INTERVAL '784 days', CURRENT_DATE - INTERVAL '783 days', 3, NULL),
    (67, 1, 9,  1, 4, 'ejecutada', 0.6, 'L/ha', CURRENT_DATE - INTERVAL '703 days', CURRENT_DATE - INTERVAL '701 days', 3, 'fungicida hoja bandera'),
    (68, 1, 10, 1, 4, 'ejecutada', 0.6, 'L/ha', CURRENT_DATE - INTERVAL '703 days', CURRENT_DATE - INTERVAL '701 days', 3, NULL),
    (69, 1, 11, 1, 4, 'ejecutada', 0.6, 'L/ha', CURRENT_DATE - INTERVAL '702 days', CURRENT_DATE - INTERVAL '701 days', 3, NULL),
    (70, 1, 12, 1, 4, 'ejecutada', 0.6, 'L/ha', CURRENT_DATE - INTERVAL '702 days', CURRENT_DATE - INTERVAL '701 days', 3, NULL),
    -- Campaña 2 (2024/2025 gruesa · soja/maíz)
    (71, 1, 8,  2, 1, 'ejecutada', 3.0, 'L/ha', CURRENT_DATE - INTERVAL '663 days', CURRENT_DATE - INTERVAL '662 days', 2, 'barbecho previo soja'),
    (72, 1, 9,  2, 1, 'ejecutada', 3.0, 'L/ha', CURRENT_DATE - INTERVAL '663 days', CURRENT_DATE - INTERVAL '662 days', 2, NULL),
    (73, 1, 10, 2, 1, 'ejecutada', 3.0, 'L/ha', CURRENT_DATE - INTERVAL '661 days', CURRENT_DATE - INTERVAL '660 days', 2, NULL),
    (74, 1, 11, 2, 1, 'ejecutada', 3.0, 'L/ha', CURRENT_DATE - INTERVAL '661 days', CURRENT_DATE - INTERVAL '660 days', 2, NULL),
    (75, 1, 12, 2, 1, 'ejecutada', 3.0, 'L/ha', CURRENT_DATE - INTERVAL '659 days', CURRENT_DATE - INTERVAL '658 days', 2, NULL),
    (76, 1, 13, 2, 1, 'ejecutada', 3.0, 'L/ha', CURRENT_DATE - INTERVAL '659 days', CURRENT_DATE - INTERVAL '658 days', 2, NULL),
    (77, 1, 14, 2, 1, 'ejecutada', 3.0, 'L/ha', CURRENT_DATE - INTERVAL '657 days', CURRENT_DATE - INTERVAL '656 days', 2, NULL),
    (78, 1, 15, 2, 6, 'ejecutada', 0.15, 'L/ha', CURRENT_DATE - INTERVAL '617 days', CURRENT_DATE - INTERVAL '616 days', 2, 'control de oruga cogollera'),
    (79, 1, 16, 2, 6, 'ejecutada', 0.15, 'L/ha', CURRENT_DATE - INTERVAL '617 days', CURRENT_DATE - INTERVAL '616 days', 2, NULL),
    (80, 1, 17, 2, 6, 'ejecutada', 0.15, 'L/ha', CURRENT_DATE - INTERVAL '615 days', CURRENT_DATE - INTERVAL '614 days', 2, NULL),
    (81, 1, 18, 2, 6, 'ejecutada', 0.15, 'L/ha', CURRENT_DATE - INTERVAL '615 days', CURRENT_DATE - INTERVAL '614 days', 2, NULL),
    (82, 1, 8,  2, 6, 'ejecutada', 0.15, 'L/ha', CURRENT_DATE - INTERVAL '613 days', CURRENT_DATE - INTERVAL '612 days', 2, NULL),
    (83, 1, 9,  2, 6, 'ejecutada', 0.15, 'L/ha', CURRENT_DATE - INTERVAL '613 days', CURRENT_DATE - INTERVAL '612 days', 2, NULL),
    (84, 1, 10, 2, 6, 'ejecutada', 0.15, 'L/ha', CURRENT_DATE - INTERVAL '611 days', CURRENT_DATE - INTERVAL '610 days', 2, NULL),
    (85, 1, 11, 2, 6, 'ejecutada', 0.15, 'L/ha', CURRENT_DATE - INTERVAL '611 days', CURRENT_DATE - INTERVAL '610 days', 2, NULL),
    (86, 1, 12, 2, 6, 'ejecutada', 0.15, 'L/ha', CURRENT_DATE - INTERVAL '609 days', CURRENT_DATE - INTERVAL '608 days', 2, NULL),
    (87, 1, 13, 2, 6, 'ejecutada', 0.15, 'L/ha', CURRENT_DATE - INTERVAL '609 days', CURRENT_DATE - INTERVAL '608 days', 2, NULL),
    (88, 1, 14, 2, 6, 'ejecutada', 0.15, 'L/ha', CURRENT_DATE - INTERVAL '607 days', CURRENT_DATE - INTERVAL '606 days', 2, NULL);

-- -----------------------------------------------------------------------------
-- Rendimientos (solo campañas históricas; la actual no se cosechó aún)
--   La helada de julio 2024 pegó en lotes 7, 8, 9 y 12 → rinden menos (demo #2)
-- -----------------------------------------------------------------------------
INSERT INTO rendimientos (id, tenant_id, campana_id, lote_id, cultivo, rendimiento_real, unidad_rendimiento, fecha_cosecha) VALUES
    -- Campaña 1 (2024/2025 fina · trigo)
    (1,  1, 1, 1,  'trigo', 3.9, 'tn/ha', CURRENT_DATE - INTERVAL '612 days'),
    (2,  1, 1, 2,  'trigo', 4.1, 'tn/ha', CURRENT_DATE - INTERVAL '612 days'),
    (3,  1, 1, 3,  'trigo', 4.0, 'tn/ha', CURRENT_DATE - INTERVAL '610 days'),
    (4,  1, 1, 4,  'trigo', 3.8, 'tn/ha', CURRENT_DATE - INTERVAL '610 days'),
    (5,  1, 1, 5,  'trigo', 4.2, 'tn/ha', CURRENT_DATE - INTERVAL '607 days'),
    (6,  1, 1, 6,  'trigo', 4.0, 'tn/ha', CURRENT_DATE - INTERVAL '607 days'),
    (7,  1, 1, 7,  'trigo', 3.2, 'tn/ha', CURRENT_DATE - INTERVAL '604 days'),  -- helada jul-2024
    (8,  1, 1, 8,  'trigo', 3.4, 'tn/ha', CURRENT_DATE - INTERVAL '604 days'),  -- helada jul-2024
    (9,  1, 1, 9,  'trigo', 3.3, 'tn/ha', CURRENT_DATE - INTERVAL '603 days'),  -- helada jul-2024
    (10, 1, 1, 10, 'trigo', 4.2, 'tn/ha', CURRENT_DATE - INTERVAL '603 days'),
    (11, 1, 1, 11, 'trigo', 4.3, 'tn/ha', CURRENT_DATE - INTERVAL '602 days'),
    (12, 1, 1, 12, 'trigo', 3.1, 'tn/ha', CURRENT_DATE - INTERVAL '602 days'),  -- helada jul-2024
    -- Campaña 2 (2024/2025 gruesa · soja/maíz)
    (13, 1, 2, 8,  'soja', 3.4, 'tn/ha', CURRENT_DATE - INTERVAL '499 days'),
    (14, 1, 2, 9,  'soja', 3.1, 'tn/ha', CURRENT_DATE - INTERVAL '499 days'),
    (15, 1, 2, 10, 'soja', 3.6, 'tn/ha', CURRENT_DATE - INTERVAL '496 days'),
    (16, 1, 2, 11, 'soja', 3.5, 'tn/ha', CURRENT_DATE - INTERVAL '496 days'),
    (17, 1, 2, 12, 'soja', 3.2, 'tn/ha', CURRENT_DATE - INTERVAL '493 days'),
    (18, 1, 2, 13, 'soja', 3.3, 'tn/ha', CURRENT_DATE - INTERVAL '493 days'),
    (19, 1, 2, 14, 'soja', 3.4, 'tn/ha', CURRENT_DATE - INTERVAL '491 days'),
    (20, 1, 2, 15, 'maiz', 8.6, 'tn/ha', CURRENT_DATE - INTERVAL '466 days'),
    (21, 1, 2, 16, 'maiz', 8.9, 'tn/ha', CURRENT_DATE - INTERVAL '466 days'),
    (22, 1, 2, 17, 'maiz', 9.1, 'tn/ha', CURRENT_DATE - INTERVAL '463 days'),
    (23, 1, 2, 18, 'maiz', 8.2, 'tn/ha', CURRENT_DATE - INTERVAL '463 days');

-- -----------------------------------------------------------------------------
-- Clima — la helada de julio 2024 (explica el rinde bajo de lotes 7-9 y 12)
-- y el clima de la campaña actual
-- -----------------------------------------------------------------------------
INSERT INTO registros_clima (id, tenant_id, lote_id, fecha, temp_min_c, temp_max_c, lluvia_mm, humedad_rel_pct) VALUES
    -- Helada (hace ~2 años): lotes 7, 8, 9 y 12 tocaron -2°C (los demás no)
    (1,  1, 7,  CURRENT_DATE - INTERVAL '760 days', -2.5, 12.0, 0.0,  NULL),
    (2,  1, 8,  CURRENT_DATE - INTERVAL '760 days', -2.0, 13.0, 0.0,  NULL),
    (3,  1, 9,  CURRENT_DATE - INTERVAL '760 days', -1.8, 12.0, 0.0,  NULL),
    (4,  1, 12, CURRENT_DATE - INTERVAL '760 days', -2.2, 12.5, 0.0,  NULL),
    (5,  1, 1,  CURRENT_DATE - INTERVAL '760 days',  1.0, 14.0, 2.4,  NULL),
    (6,  1, 2,  CURRENT_DATE - INTERVAL '760 days',  0.8, 15.0, 1.8,  NULL),
    (7,  1, 3,  CURRENT_DATE - INTERVAL '760 days',  1.2, 14.0, 3.1,  NULL),
    (8,  1, 5,  CURRENT_DATE - INTERVAL '760 days',  0.5, 14.5, 2.0,  NULL),
    -- Clima campaña actual: lluvias del mes pasado, seco en los últimos días
    (9,  1, 1,  CURRENT_DATE - INTERVAL '35 days',  4.0, 16.0, 12.5, 68),
    (10, 1, 4,  CURRENT_DATE - INTERVAL '35 days',  3.5, 15.0, 8.2,  65),
    (11, 1, 7,  CURRENT_DATE - INTERVAL '35 days',  3.0, 14.0, 6.0,  62),
    (12, 1, 12, CURRENT_DATE - INTERVAL '35 days',  3.8, 16.0, 10.4, 66),
    (13, 1, 1,  CURRENT_DATE - INTERVAL '9 days',   6.0, 18.0, 0.0,  58),
    (14, 1, 4,  CURRENT_DATE - INTERVAL '9 days',   5.5, 19.0, 0.0,  55),
    (15, 1, 7,  CURRENT_DATE - INTERVAL '9 days',   5.0, 17.0, 1.2,  57),
    (16, 1, 12, CURRENT_DATE - INTERVAL '9 days',   5.8, 18.0, 0.0,  56),
    (17, 1, 1,  CURRENT_DATE - INTERVAL '2 days',   7.0, 21.0, 0.0,  50),
    (18, 1, 12, CURRENT_DATE - INTERVAL '2 days',   7.5, 22.0, 0.0,  48);

-- -----------------------------------------------------------------------------
-- Documentos (RAG — SOLO documentos, nunca datos estructurados)
-- -----------------------------------------------------------------------------
INSERT INTO documentos (id, tenant_id, filename, content_text, metadata) VALUES
    (1, 1, 'manual-buenas-practicas.txt',
     'Manual de buenas prácticas de la cooperativa. El barbecho químico debe realizarse al menos 15 días antes de la siembra. Las aplicaciones de herbicidas post-emergencia en trigo se realizan con el cultivo en macollaje, evitando días con viento superior a 15 km/h. El monitoreo de plagas se realiza cada 7 días durante el ciclo. Los fertilizantes fosfatados se aplican a la siembra, y el nitrógeno puede fraccionarse entre siembra y macollaje.',
     '{"tipo": "manual", "idioma": "es"}'),
    (2, 1, 'informe-campana-2024-2025.txt',
     'Informe de cierre de la campaña 2024/2025. El trigo rindió en promedio 3.8 tn/ha, 8% por debajo del objetivo, con pérdidas concentradas en lotes 7, 8, 9 y 12 por la helada del 15 de julio de 2024 (-2°C). La soja rindió 3.4 tn/ha promedio y el maíz 8.7 tn/ha. Se detectaron demoras en el control de malezas post-emergencia en lotes 4 y 12. Recomendación para 2026/2027: adelantar el barbecho y verificar cobertura de seguro de granizo en lotes bajos.',
     '{"tipo": "informe", "campaña": "2024/2025", "idioma": "es"}'),
    (3, 1, 'protocolo-herbicidas-trigo.txt',
     'Protocolo de herbicidas para trigo. Glifosato 48%: 3 L/ha en barbecho. 2,4-D amina: 1 L/ha en post-emergencia temprana (macollaje), NO aplicar en etapas reproductivas. Metsulfurón: 6 g/ha solo con buena humedad de suelo. Respetar el intervalo mínimo de 10 días entre la aplicación de 2,4-D y la emergencia del cultivo siguiente.',
     '{"tipo": "protocolo", "idioma": "es"}');

-- -----------------------------------------------------------------------------
-- Resincronizar secuencias (BIGSERIAL)
-- El seed inserta con ids EXPLÍCITOS (para que las historias demo referencien
-- ids estables). Sin este paso, la secuencia queda en 1 y el primer INSERT
-- real (p.ej. el HITL al aprobar) choca con duplicate key. Es la corrección
-- de raíz: la DB actual también se arregla con setval.
-- -----------------------------------------------------------------------------
SELECT setval('tenants_id_seq', (SELECT max(id) FROM tenants));
SELECT setval('users_id_seq', (SELECT max(id) FROM users));
SELECT setval('lotes_id_seq', (SELECT max(id) FROM lotes));
SELECT setval('campanas_id_seq', (SELECT max(id) FROM campanas));
SELECT setval('campana_lotes_id_seq', (SELECT max(id) FROM campana_lotes));
SELECT setval('productos_id_seq', (SELECT max(id) FROM productos));
SELECT setval('aplicaciones_id_seq', (SELECT max(id) FROM aplicaciones));
SELECT setval('rendimientos_id_seq', (SELECT max(id) FROM rendimientos));
SELECT setval('registros_clima_id_seq', (SELECT max(id) FROM registros_clima));
SELECT setval('documentos_id_seq', (SELECT max(id) FROM documentos));
SELECT setval('conversations_id_seq', (SELECT max(id) FROM conversations));
SELECT setval('messages_id_seq', (SELECT max(id) FROM messages));
SELECT setval('audit_log_id_seq', (SELECT max(id) FROM audit_log));

COMMIT;