package main

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	mrand "math/rand"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"golang.org/x/crypto/bcrypt"
)

type Config struct {
	Port   string
	DBHost string
	DBPort string
	DBUser string
	DBPass string
	DBName string
}

func loadConfig() Config {
	return Config{
		Port:   getEnv("PORT", "8080"),
		DBHost: getEnv("DB_HOST", "localhost"),
		DBPort: getEnv("DB_PORT", "3306"),
		DBUser: getEnv("DB_USER", "root"),
		DBPass: getEnv("DB_PASS", ""),
		DBName: getEnv("DB_NAME", "railway"),
	}
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// ── Models ──

type Reparacion struct {
	ID             int        `json:"id"`
	Codigo         string     `json:"codigo"`
	NombreCliente  string     `json:"nombre_cliente"`
	Telefono       string     `json:"telefono"`
	Email          string     `json:"email"`
	ConsolaSlug    string     `json:"consola"`
	Problema       string     `json:"problema"`
	Diagnostico    *string    `json:"diagnostico"`
	PrecioCotizado *float64   `json:"precio_cotizado"`
	PrecioFinal    *float64   `json:"precio_final"`
	Estado         string     `json:"estado"`
	Prioridad      string     `json:"prioridad"`
	FechaIngreso   time.Time  `json:"fecha_ingreso"`
	FechaEntrega   *time.Time `json:"fecha_entrega"`
	GarantiaMeses  int        `json:"garantia_meses"`
	NotasTecnico   *string    `json:"notas_tecnico"`
	MetodoPago     *string    `json:"metodo_pago"`
}

type NuevaReparacion struct {
	Nombre   string `json:"nombre"`
	Telefono string `json:"telefono"`
	Email    string `json:"email"`
	Consola  string `json:"consola"`
	Problema string `json:"problema"`
}

type ActualizarEstado struct {
	Estado         string   `json:"estado"`
	Diagnostico    *string  `json:"diagnostico,omitempty"`
	PrecioCotizado *float64 `json:"precio_cotizado,omitempty"`
	GarantiaMeses  *int     `json:"garantia_meses,omitempty"`
	NotasTecnico   *string  `json:"notas_tecnico,omitempty"`
	MetodoPago     *string  `json:"metodo_pago,omitempty"`
}

type Consola struct {
	ID     int    `json:"id"`
	Nombre string `json:"nombre"`
	Marca  string `json:"marca"`
	Slug   string `json:"slug"`
}

type Servicio struct {
	ID            int     `json:"id"`
	Nombre        string  `json:"nombre"`
	Descripcion   *string `json:"descripcion"`
	PrecioBase    float64 `json:"precio_base"`
	DuracionHoras int     `json:"duracion_horas"`
}

type Stats struct {
	TotalReparaciones  int     `json:"total_reparaciones"`
	Pendientes         int     `json:"pendientes"`
	EnProceso          int     `json:"en_proceso"`
	Completadas        int     `json:"completadas"`
	IngresoTotal       float64 `json:"ingreso_total"`
	PromedioSatisfecho float64 `json:"promedio_satisfecho"`
}

type APIResponse struct {
	Success bool        `json:"success"`
	Data    interface{} `json:"data,omitempty"`
	Error   string      `json:"error,omitempty"`
	Codigo  string      `json:"codigo,omitempty"`
}

// ── Fotos Model ──

type ConsolaFoto struct {
	ID         int       `json:"id"`
	CodigoRep  string    `json:"codigo_rep"`
	FotoBase64 string    `json:"foto"`
	CreatedAt  time.Time `json:"created_at"`
}

type SubirFotosRequest struct {
	Fotos []string `json:"fotos"`
}

// ── Auth Models ──

type GoogleLoginRequest struct {
	Email    string `json:"email"`
	Nombre   string `json:"nombre"`
	Foto     string `json:"foto"`
	GoogleID string `json:"google_id"`
}

type ManualLoginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type RegisterRequest struct {
	Nombre   string `json:"nombre"`
	Email    string `json:"email"`
	Telefono string `json:"telefono"`
	Password string `json:"password"`
}

type AuthResponse struct {
	Success bool   `json:"success"`
	Token   string `json:"token,omitempty"`
	Nombre  string `json:"nombre,omitempty"`
	Email   string `json:"email,omitempty"`
	Rol     string `json:"rol,omitempty"`
	Foto    string `json:"foto,omitempty"`
	Error   string `json:"error,omitempty"`
}

type Session struct {
	Token     string
	UserID    int
	Email     string
	Nombre    string
	Rol       string
	Foto      string
	CreatedAt time.Time
}

var (
	sessions   = make(map[string]*Session)
	sessionsMu sync.RWMutex
)

func generateToken() string {
	bytes := make([]byte, 32)
	rand.Read(bytes)
	return hex.EncodeToString(bytes)
}

func createSession(userID int, email, nombre, rol, foto string) string {
	token := generateToken()
	sessionsMu.Lock()
	sessions[token] = &Session{
		Token: token, UserID: userID, Email: email,
		Nombre: nombre, Rol: rol, Foto: foto,
		CreatedAt: time.Now(),
	}
	sessionsMu.Unlock()
	return token
}

func getSessionByToken(token string) *Session {
	sessionsMu.RLock()
	defer sessionsMu.RUnlock()
	s, ok := sessions[token]
	if !ok {
		return nil
	}
	if time.Since(s.CreatedAt) > 24*time.Hour {
		delete(sessions, token)
		return nil
	}
	return s
}

func deleteSession(token string) {
	sessionsMu.Lock()
	delete(sessions, token)
	sessionsMu.Unlock()
}

var db *sql.DB

func initDB(cfg Config) {
	dsn := fmt.Sprintf("%s:%s@tcp(%s:%s)/%s?parseTime=true&charset=utf8mb4&loc=Local",
		cfg.DBUser, cfg.DBPass, cfg.DBHost, cfg.DBPort, cfg.DBName,
	)
	var err error
	db, err = sql.Open("mysql", dsn)
	if err != nil {
		log.Fatalf("Error conectando a MySQL: %v", err)
	}
	db.SetMaxOpenConns(25)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(5 * time.Minute)
	if err = db.Ping(); err != nil {
		log.Fatalf("No se pudo hacer ping a MySQL: %v", err)
	}
	log.Println("✅ Conectado a MySQL correctamente")
}

func generarCodigo() string {
	chars := "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	code := "JTS-"
	for i := 0; i < 6; i++ {
		code += string(chars[mrand.Intn(len(chars))])
	}
	return code
}

func writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, APIResponse{Success: false, Error: msg})
}

func extractToken(r *http.Request) string {
	auth := r.Header.Get("Authorization")
	if strings.HasPrefix(auth, "Bearer ") {
		return strings.TrimPrefix(auth, "Bearer ")
	}
	return r.URL.Query().Get("token")
}

// ── NUEVO: Middleware de seguridad ──
// Agrega todos los headers HTTP de seguridad a cada respuesta.
func securityMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Fuerza HTTPS y evita downgrade a HTTP
		w.Header().Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains; preload")
		// Evita que el sitio sea embebido en iframes ajenos (anti-Clickjacking)
		w.Header().Set("X-Frame-Options", "SAMEORIGIN")
		// Evita que el navegador adivine el tipo de contenido
		w.Header().Set("X-Content-Type-Options", "nosniff")
		// Protección XSS básica para navegadores antiguos
		w.Header().Set("X-XSS-Protection", "1; mode=block")
		// Controla qué información de referencia se comparte
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		// Restringe acceso a funciones del navegador
		w.Header().Set("Permissions-Policy", "geolocation=(), microphone=(), camera=(), payment=(), usb=()")
		// Content Security Policy: ajusta los dominios si usas CDNs externos
		w.Header().Set("Content-Security-Policy",
			"default-src 'self'; "+
				"script-src 'self' 'unsafe-inline' 'unsafe-eval' https://accounts.google.com; "+
				"style-src 'self' 'unsafe-inline' https://fonts.googleapis.com; "+
				"font-src 'self' https://fonts.gstatic.com; "+
				"img-src 'self' data: https:; "+
				"connect-src 'self' https://accounts.google.com; "+
				"frame-src https://accounts.google.com; "+
				"frame-ancestors 'self'; "+
				"base-uri 'self'; "+
				"form-action 'self';")
		// Oculta información del servidor
		w.Header().Del("X-Powered-By")
		next(w, r)
	}
}

func corsMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PATCH, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}
		next(w, r)
	}
}

func logMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next(w, r)
		log.Printf("%-6s %s %v", r.Method, r.URL.Path, time.Since(start))
	}
}

// ── FOTOS HANDLERS ──

func fotosHandler(w http.ResponseWriter, r *http.Request) {
	codigo := strings.TrimPrefix(r.URL.Path, "/api/fotos/")
	codigo = strings.TrimSuffix(codigo, "/")
	if codigo == "" {
		writeError(w, http.StatusBadRequest, "Código requerido")
		return
	}
	switch r.Method {
	case http.MethodGet:
		obtenerFotos(w, r, codigo)
	case http.MethodPost:
		subirFotos(w, r, codigo)
	case http.MethodDelete:
		eliminarFotos(w, r, codigo)
	default:
		writeError(w, http.StatusMethodNotAllowed, "Método no permitido")
	}
}

func obtenerFotos(w http.ResponseWriter, r *http.Request, codigo string) {
	rows, err := db.Query(
		"SELECT id, codigo_rep, foto_base64, created_at FROM consola_fotos WHERE codigo_rep = ? ORDER BY id ASC",
		codigo,
	)
	if err != nil {
		log.Printf("Error consultando fotos para %s: %v", codigo, err)
		writeError(w, http.StatusInternalServerError, "Error consultando fotos")
		return
	}
	defer rows.Close()
	var fotos []ConsolaFoto
	for rows.Next() {
		var f ConsolaFoto
		if err := rows.Scan(&f.ID, &f.CodigoRep, &f.FotoBase64, &f.CreatedAt); err != nil {
			continue
		}
		fotos = append(fotos, f)
	}
	if fotos == nil {
		fotos = []ConsolaFoto{}
	}
	writeJSON(w, http.StatusOK, APIResponse{Success: true, Data: fotos})
}

func subirFotos(w http.ResponseWriter, r *http.Request, codigo string) {
	var exists int
	if err := db.QueryRow("SELECT COUNT(*) FROM reparaciones WHERE codigo = ?", codigo).Scan(&exists); err != nil || exists == 0 {
		writeError(w, http.StatusNotFound, "Reparación no encontrada")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 50<<20) // 50MB

	var req SubirFotosRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		log.Printf("Error decodificando fotos para %s: %v", codigo, err)
		writeError(w, http.StatusBadRequest, "JSON inválido o demasiado grande")
		return
	}
	if len(req.Fotos) == 0 {
		writeError(w, http.StatusBadRequest, "No se enviaron fotos")
		return
	}
	if len(req.Fotos) > 10 {
		req.Fotos = req.Fotos[:10]
	}

	stmt, err := db.Prepare("INSERT INTO consola_fotos (codigo_rep, foto_base64) VALUES (?, ?)")
	if err != nil {
		log.Printf("Error preparando inserción fotos: %v", err)
		writeError(w, http.StatusInternalServerError, "Error preparando inserción")
		return
	}
	defer stmt.Close()

	inserted := 0
	for _, foto := range req.Fotos {
		if foto == "" {
			continue
		}
		if _, err := stmt.Exec(codigo, foto); err != nil {
			log.Printf("Error insertando foto para %s: %v", codigo, err)
			continue
		}
		inserted++
	}

	log.Printf("📸 %d fotos subidas para %s", inserted, codigo)
	writeJSON(w, http.StatusCreated, APIResponse{
		Success: true,
		Data:    map[string]int{"subidas": inserted},
	})
}

func eliminarFotos(w http.ResponseWriter, r *http.Request, codigo string) {
	res, err := db.Exec("DELETE FROM consola_fotos WHERE codigo_rep = ?", codigo)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Error eliminando fotos")
		return
	}
	n, _ := res.RowsAffected()
	writeJSON(w, http.StatusOK, APIResponse{
		Success: true,
		Data:    map[string]int64{"eliminadas": n},
	})
}

// ── AUTH HANDLERS ──

func authGoogle(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "Método no permitido")
		return
	}
	var req GoogleLoginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, AuthResponse{Success: false, Error: "JSON inválido"})
		return
	}
	if req.Email == "" {
		writeJSON(w, http.StatusBadRequest, AuthResponse{Success: false, Error: "Email requerido"})
		return
	}
	var userID int
	var nombre, rol string
	err := db.QueryRow("SELECT id, nombre, rol FROM usuarios WHERE email = ? AND activo = 1", req.Email).
		Scan(&userID, &nombre, &rol)
	if err == sql.ErrNoRows {
		result, err := db.Exec(
			`INSERT INTO usuarios (nombre, email, password_hash, rol, google_id, foto, ultimo_login)
			 VALUES (?, ?, '', 'cliente', ?, ?, NOW())`,
			req.Nombre, req.Email, req.GoogleID, req.Foto,
		)
		if err != nil {
			log.Printf("Error creando usuario Google: %v", err)
			writeJSON(w, http.StatusInternalServerError, AuthResponse{Success: false, Error: "Error al registrar"})
			return
		}
		id, _ := result.LastInsertId()
		userID = int(id)
		nombre = req.Nombre
		rol = "cliente"
		db.Exec("INSERT IGNORE INTO clientes (nombre, telefono, email) VALUES (?, '', ?)", req.Nombre, req.Email)
		log.Printf("🆕 Nuevo cliente Google: %s (%s)", req.Nombre, req.Email)
	} else if err != nil {
		writeJSON(w, http.StatusInternalServerError, AuthResponse{Success: false, Error: "Error del servidor"})
		return
	} else {
		db.Exec("UPDATE usuarios SET ultimo_login = NOW(), foto = ?, google_id = ? WHERE id = ?",
			req.Foto, req.GoogleID, userID)
		log.Printf("🔑 Login Google: %s (%s) — %s", nombre, req.Email, rol)
	}
	token := createSession(userID, req.Email, nombre, rol, req.Foto)
	writeJSON(w, http.StatusOK, AuthResponse{
		Success: true, Token: token, Nombre: nombre,
		Email: req.Email, Rol: rol, Foto: req.Foto,
	})
}

func authLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "Método no permitido")
		return
	}
	var req ManualLoginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, AuthResponse{Success: false, Error: "JSON inválido"})
		return
	}
	if req.Email == "" || req.Password == "" {
		writeJSON(w, http.StatusBadRequest, AuthResponse{Success: false, Error: "Email y contraseña requeridos"})
		return
	}
	var userID int
	var nombre, rol, passwordHash string
	var foto sql.NullString
	err := db.QueryRow("SELECT id, nombre, password_hash, rol, foto FROM usuarios WHERE email = ? AND activo = 1", req.Email).
		Scan(&userID, &nombre, &passwordHash, &rol, &foto)
	if err == sql.ErrNoRows {
		writeJSON(w, http.StatusUnauthorized, AuthResponse{Success: false, Error: "Credenciales incorrectas"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, AuthResponse{Success: false, Error: "Error del servidor"})
		return
	}
	if err := bcrypt.CompareHashAndPassword([]byte(passwordHash), []byte(req.Password)); err != nil {
		writeJSON(w, http.StatusUnauthorized, AuthResponse{Success: false, Error: "Credenciales incorrectas"})
		return
	}
	db.Exec("UPDATE usuarios SET ultimo_login = NOW() WHERE id = ?", userID)
	log.Printf("🔑 Login manual: %s (%s) — %s", nombre, req.Email, rol)
	fotoStr := ""
	if foto.Valid {
		fotoStr = foto.String
	}
	token := createSession(userID, req.Email, nombre, rol, fotoStr)
	writeJSON(w, http.StatusOK, AuthResponse{
		Success: true, Token: token, Nombre: nombre,
		Email: req.Email, Rol: rol,
	})
}

func authMe(w http.ResponseWriter, r *http.Request) {
	token := extractToken(r)
	if token == "" {
		writeJSON(w, http.StatusUnauthorized, AuthResponse{Success: false, Error: "No autenticado"})
		return
	}
	session := getSessionByToken(token)
	if session == nil {
		writeJSON(w, http.StatusUnauthorized, AuthResponse{Success: false, Error: "Sesión expirada"})
		return
	}
	writeJSON(w, http.StatusOK, AuthResponse{
		Success: true, Nombre: session.Nombre,
		Email: session.Email, Rol: session.Rol, Foto: session.Foto,
	})
}

func authLogout(w http.ResponseWriter, r *http.Request) {
	token := extractToken(r)
	if token != "" {
		deleteSession(token)
	}
	writeJSON(w, http.StatusOK, APIResponse{Success: true, Data: "Sesión cerrada"})
}

func authRegister(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "Método no permitido")
		return
	}
	var req RegisterRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, AuthResponse{Success: false, Error: "JSON inválido"})
		return
	}
	if req.Nombre == "" || req.Email == "" || req.Password == "" || req.Telefono == "" {
		writeJSON(w, http.StatusBadRequest, AuthResponse{Success: false, Error: "Todos los campos son obligatorios"})
		return
	}
	if len(req.Password) < 6 {
		writeJSON(w, http.StatusBadRequest, AuthResponse{Success: false, Error: "La contraseña debe tener al menos 6 caracteres"})
		return
	}
	var exists int
	db.QueryRow("SELECT COUNT(*) FROM usuarios WHERE email = ?", req.Email).Scan(&exists)
	if exists > 0 {
		writeJSON(w, http.StatusConflict, AuthResponse{Success: false, Error: "Ya existe una cuenta con este correo"})
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, AuthResponse{Success: false, Error: "Error del servidor"})
		return
	}
	result, err := db.Exec(
		`INSERT INTO usuarios (nombre, email, password_hash, rol, ultimo_login) VALUES (?, ?, ?, 'cliente', NOW())`,
		req.Nombre, req.Email, string(hash),
	)
	if err != nil {
		log.Printf("Error registrando usuario: %v", err)
		writeJSON(w, http.StatusInternalServerError, AuthResponse{Success: false, Error: "Error al crear la cuenta"})
		return
	}
	db.Exec("INSERT IGNORE INTO clientes (nombre, telefono, email) VALUES (?, ?, ?)", req.Nombre, req.Telefono, req.Email)
	id, _ := result.LastInsertId()
	token := createSession(int(id), req.Email, req.Nombre, "cliente", "")
	log.Printf("🆕 Nuevo cliente registrado: %s (%s)", req.Nombre, req.Email)
	writeJSON(w, http.StatusCreated, AuthResponse{
		Success: true, Token: token, Nombre: req.Nombre,
		Email: req.Email, Rol: "cliente",
	})
}

func reparacionesPorCliente(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "Método no permitido")
		return
	}
	email := strings.TrimPrefix(r.URL.Path, "/api/reparaciones/cliente/")
	if email == "" {
		writeError(w, http.StatusBadRequest, "Email requerido")
		return
	}
	rows, err := db.Query(`SELECT id, codigo, nombre_cliente, telefono, email, consola_slug,
		problema, diagnostico, precio_cotizado, precio_final, estado, prioridad,
		fecha_ingreso, fecha_entrega, garantia_meses, notas_tecnico, metodo_pago
		FROM reparaciones WHERE email = ? ORDER BY fecha_ingreso DESC`, email)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Error consultando reparaciones")
		return
	}
	defer rows.Close()
	var reps []Reparacion
	for rows.Next() {
		var rep Reparacion
		if err := rows.Scan(&rep.ID, &rep.Codigo, &rep.NombreCliente, &rep.Telefono, &rep.Email,
			&rep.ConsolaSlug, &rep.Problema, &rep.Diagnostico, &rep.PrecioCotizado,
			&rep.PrecioFinal, &rep.Estado, &rep.Prioridad, &rep.FechaIngreso,
			&rep.FechaEntrega, &rep.GarantiaMeses, &rep.NotasTecnico, &rep.MetodoPago); err != nil {
			continue
		}
		reps = append(reps, rep)
	}
	writeJSON(w, http.StatusOK, APIResponse{Success: true, Data: reps})
}

func crearReparacion(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "Método no permitido")
		return
	}
	var req NuevaReparacion
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "JSON inválido")
		return
	}
	if req.Nombre == "" || req.Telefono == "" || req.Email == "" || req.Consola == "" || req.Problema == "" {
		writeError(w, http.StatusBadRequest, "Todos los campos son obligatorios")
		return
	}
	codigo := generarCodigo()
	_, err := db.Exec(`INSERT INTO reparaciones
		(codigo, nombre_cliente, telefono, email, consola_slug, problema, estado, prioridad)
		VALUES (?, ?, ?, ?, ?, ?, 'pendiente', 'normal')`,
		codigo, req.Nombre, req.Telefono, req.Email, req.Consola, req.Problema)
	if err != nil {
		log.Printf("Error insertando reparación: %v", err)
		writeError(w, http.StatusInternalServerError, "Error al registrar la solicitud")
		return
	}
	db.Exec(`INSERT INTO historial_estados (reparacion_id, estado_nuevo, comentario)
		SELECT id, 'pendiente', 'Solicitud recibida desde la web' FROM reparaciones WHERE codigo = ?`, codigo)
	writeJSON(w, http.StatusCreated, APIResponse{Success: true, Codigo: codigo, Data: map[string]string{"mensaje": "Solicitud registrada exitosamente"}})
}

func listarReparaciones(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "Método no permitido")
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/api/reparaciones")
	path = strings.TrimPrefix(path, "/")
	if path != "" {
		consultarReparacion(w, r, path)
		return
	}
	estado := r.URL.Query().Get("estado")
	query := `SELECT id, codigo, nombre_cliente, telefono, email, consola_slug,
		problema, diagnostico, precio_cotizado, precio_final, estado, prioridad,
		fecha_ingreso, fecha_entrega, garantia_meses, notas_tecnico, metodo_pago FROM reparaciones`
	args := []interface{}{}
	if estado != "" {
		query += " WHERE estado = ?"
		args = append(args, estado)
	}
	query += " ORDER BY fecha_ingreso DESC LIMIT 100"
	rows, err := db.Query(query, args...)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Error consultando reparaciones")
		return
	}
	defer rows.Close()
	var reparaciones []Reparacion
	for rows.Next() {
		var rep Reparacion
		if err := rows.Scan(&rep.ID, &rep.Codigo, &rep.NombreCliente, &rep.Telefono, &rep.Email,
			&rep.ConsolaSlug, &rep.Problema, &rep.Diagnostico, &rep.PrecioCotizado,
			&rep.PrecioFinal, &rep.Estado, &rep.Prioridad, &rep.FechaIngreso,
			&rep.FechaEntrega, &rep.GarantiaMeses, &rep.NotasTecnico, &rep.MetodoPago); err != nil {
			continue
		}
		reparaciones = append(reparaciones, rep)
	}
	writeJSON(w, http.StatusOK, APIResponse{Success: true, Data: reparaciones})
}

func consultarReparacion(w http.ResponseWriter, r *http.Request, codigo string) {
	var rep Reparacion
	err := db.QueryRow(`SELECT id, codigo, nombre_cliente, telefono, email, consola_slug,
		problema, diagnostico, precio_cotizado, precio_final, estado, prioridad,
		fecha_ingreso, fecha_entrega, garantia_meses, notas_tecnico, metodo_pago
		FROM reparaciones WHERE codigo = ?`, codigo).Scan(
		&rep.ID, &rep.Codigo, &rep.NombreCliente, &rep.Telefono, &rep.Email,
		&rep.ConsolaSlug, &rep.Problema, &rep.Diagnostico, &rep.PrecioCotizado,
		&rep.PrecioFinal, &rep.Estado, &rep.Prioridad, &rep.FechaIngreso,
		&rep.FechaEntrega, &rep.GarantiaMeses, &rep.NotasTecnico, &rep.MetodoPago)
	if err == sql.ErrNoRows {
		writeError(w, http.StatusNotFound, "Reparación no encontrada")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Error consultando reparación")
		return
	}
	writeJSON(w, http.StatusOK, APIResponse{Success: true, Data: rep})
}

func actualizarReparacion(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPatch {
		writeError(w, http.StatusMethodNotAllowed, "Método no permitido")
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/api/reparaciones/update/")
	if path == "" {
		writeError(w, http.StatusBadRequest, "Código requerido")
		return
	}
	var req ActualizarEstado
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "JSON inválido")
		return
	}
	validStates := map[string]bool{
		"pendiente": true, "diagnostico": true, "cotizado": true, "aprobado": true,
		"en_reparacion": true, "reparado": true, "entregado": true, "cancelado": true,
	}
	if !validStates[req.Estado] {
		writeError(w, http.StatusBadRequest, "Estado inválido")
		return
	}
	var estadoActual string
	if err := db.QueryRow("SELECT estado FROM reparaciones WHERE codigo = ?", path).Scan(&estadoActual); err == sql.ErrNoRows {
		writeError(w, http.StatusNotFound, "Reparación no encontrada")
		return
	}
	query := "UPDATE reparaciones SET estado = ?"
	args := []interface{}{req.Estado}
	if req.Diagnostico != nil {
		query += ", diagnostico = ?"
		args = append(args, *req.Diagnostico)
	}
	if req.PrecioCotizado != nil {
		query += ", precio_cotizado = ?"
		args = append(args, *req.PrecioCotizado)
	}
	if req.GarantiaMeses != nil {
		query += ", garantia_meses = ?"
		args = append(args, *req.GarantiaMeses)
	}
	if req.NotasTecnico != nil {
		query += ", notas_tecnico = ?"
		args = append(args, *req.NotasTecnico)
	}
	if req.MetodoPago != nil {
		query += ", metodo_pago = ?"
		args = append(args, *req.MetodoPago)
	}
	if req.Estado == "entregado" {
		query += ", fecha_entrega = NOW()"
	}
	if req.Estado == "cotizado" {
		query += ", fecha_cotizacion = NOW()"
	}
	if req.Estado == "aprobado" {
		query += ", fecha_aprobacion = NOW()"
	}
	query += " WHERE codigo = ?"
	args = append(args, path)
	db.Exec(query, args...)
	db.Exec(`INSERT INTO historial_estados (reparacion_id, estado_anterior, estado_nuevo)
		SELECT id, ?, ? FROM reparaciones WHERE codigo = ?`, estadoActual, req.Estado, path)
	writeJSON(w, http.StatusOK, APIResponse{Success: true, Data: map[string]string{"mensaje": "Estado actualizado", "estado": req.Estado}})
}

func listarConsolas(w http.ResponseWriter, r *http.Request) {
	rows, err := db.Query("SELECT id, nombre, marca, slug FROM consolas WHERE activo = 1 ORDER BY marca, nombre")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Error")
		return
	}
	defer rows.Close()
	var list []Consola
	for rows.Next() {
		var c Consola
		rows.Scan(&c.ID, &c.Nombre, &c.Marca, &c.Slug)
		list = append(list, c)
	}
	writeJSON(w, http.StatusOK, APIResponse{Success: true, Data: list})
}

func listarServicios(w http.ResponseWriter, r *http.Request) {
	rows, err := db.Query("SELECT id, nombre, descripcion, precio_base, duracion_horas FROM servicios WHERE activo = 1")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Error")
		return
	}
	defer rows.Close()
	var list []Servicio
	for rows.Next() {
		var s Servicio
		rows.Scan(&s.ID, &s.Nombre, &s.Descripcion, &s.PrecioBase, &s.DuracionHoras)
		list = append(list, s)
	}
	writeJSON(w, http.StatusOK, APIResponse{Success: true, Data: list})
}

func obtenerStats(w http.ResponseWriter, r *http.Request) {
	var s Stats
	db.QueryRow("SELECT COUNT(*) FROM reparaciones").Scan(&s.TotalReparaciones)
	db.QueryRow("SELECT COUNT(*) FROM reparaciones WHERE estado = 'pendiente'").Scan(&s.Pendientes)
	db.QueryRow("SELECT COUNT(*) FROM reparaciones WHERE estado IN ('diagnostico','cotizado','aprobado','en_reparacion')").Scan(&s.EnProceso)
	db.QueryRow("SELECT COUNT(*) FROM reparaciones WHERE estado IN ('reparado','entregado')").Scan(&s.Completadas)
	db.QueryRow("SELECT COALESCE(SUM(precio_final), 0) FROM reparaciones WHERE estado = 'entregado'").Scan(&s.IngresoTotal)
	if s.TotalReparaciones > 0 {
		s.PromedioSatisfecho = float64(s.Completadas) / float64(s.TotalReparaciones) * 100
	}
	writeJSON(w, http.StatusOK, APIResponse{Success: true, Data: s})
}

func healthCheck(w http.ResponseWriter, r *http.Request) {
	status := "ok"
	if err := db.Ping(); err != nil {
		status = "error"
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": status, "service": "Junior Technical Services API", "version": "2.0.0"})
}

func main() {
	cfg := loadConfig()
	initDB(cfg)
	defer db.Close()

	// ── Migraciones automáticas ──
	// NOTA: ALTER TABLE ADD COLUMN no soporta IF NOT EXISTS en MySQL.
	// Si la columna ya existe, el error se ignora silenciosamente.
	migrations := []string{
		"ALTER TABLE usuarios ADD COLUMN google_id VARCHAR(100) DEFAULT NULL",
		"ALTER TABLE usuarios ADD COLUMN foto VARCHAR(500) DEFAULT NULL",
		"ALTER TABLE reparaciones ADD COLUMN metodo_pago VARCHAR(20) DEFAULT NULL",
		`CREATE TABLE IF NOT EXISTS consola_fotos (
			id          INT AUTO_INCREMENT PRIMARY KEY,
			codigo_rep  VARCHAR(20) NOT NULL,
			foto_base64 LONGTEXT    NOT NULL,
			created_at  TIMESTAMP   DEFAULT CURRENT_TIMESTAMP,
			INDEX idx_codigo (codigo_rep)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`,
	}
	for _, m := range migrations {
		if _, err := db.Exec(m); err != nil {
			log.Printf("⚠️  Migración: %v", err)
		}
	}
	log.Println("✅ Migraciones aplicadas")

	// ── Routes ──
	// NOTA: securityMiddleware se aplica a todas las rutas para garantizar
	// que cada respuesta incluya los headers de seguridad HTTP.
	chain := func(h http.HandlerFunc) http.HandlerFunc {
		return logMiddleware(securityMiddleware(corsMiddleware(h)))
	}

	http.HandleFunc("/api/auth/google", chain(authGoogle))
	http.HandleFunc("/api/auth/login", chain(authLogin))
	http.HandleFunc("/api/auth/register", chain(authRegister))
	http.HandleFunc("/api/auth/me", chain(authMe))
	http.HandleFunc("/api/auth/logout", chain(authLogout))
	http.HandleFunc("/api/health", chain(healthCheck))
	http.HandleFunc("/api/fotos/", chain(fotosHandler))
	http.HandleFunc("/api/reparaciones/cliente/", chain(reparacionesPorCliente))
	http.HandleFunc("/api/reparaciones/update/", chain(actualizarReparacion))
	http.HandleFunc("/api/reparaciones", chain(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost:
			crearReparacion(w, r)
		case http.MethodGet:
			listarReparaciones(w, r)
		default:
			writeError(w, http.StatusMethodNotAllowed, "Método no permitido")
		}
	}))
	http.HandleFunc("/api/reparaciones/", chain(listarReparaciones))
	http.HandleFunc("/api/consolas", chain(listarConsolas))
	http.HandleFunc("/api/servicios", chain(listarServicios))
	http.HandleFunc("/api/stats", chain(obtenerStats))

	addr := ":" + cfg.Port
	log.Printf("🚀 Junior Technical Services API v2.0 en http://localhost%s", addr)
	log.Printf("🔒 Headers de seguridad activos: HSTS, CSP, X-Frame-Options, X-Content-Type-Options")
	log.Printf("📋 Endpoints:")
	log.Printf("   POST   /api/fotos/{codigo}")
	log.Printf("   GET    /api/fotos/{codigo}")
	log.Printf("   DELETE /api/fotos/{codigo}")
	log.Printf("   POST   /api/auth/google")
	log.Printf("   POST   /api/auth/login")
	log.Printf("   POST   /api/auth/register")
	log.Printf("   GET    /api/auth/me")
	log.Printf("   POST   /api/auth/logout")
	log.Printf("   POST   /api/reparaciones")
	log.Printf("   GET    /api/reparaciones")
	log.Printf("   GET    /api/reparaciones/{codigo}")
	log.Printf("   GET    /api/reparaciones/cliente/{email}")
	log.Printf("   PATCH  /api/reparaciones/update/{codigo}")
	log.Printf("   GET    /api/consolas")
	log.Printf("   GET    /api/servicios")
	log.Printf("   GET    /api/stats")
	log.Printf("   GET    /api/health")

	if err := http.ListenAndServe(addr, nil); err != nil {
		log.Fatalf("Error iniciando servidor: %v", err)
	}
}
