package i18n

func init() {
	Register("questionnaire", map[string]string{
		"chat_b.question.hint_questionnaire":                 "Up/Down select | Enter answer | Left/Right or Tab change question | Esc cancel",
		"chat_b.question.hint_questionnaire_other":           "Type a custom answer | Enter save | Esc close editor",
		"chat_b.question.hint_questionnaire_review":          "1-9 edit answer | Left edit last | Enter submit | Esc cancel",
		"chat_b.question.hint_questionnaire_incomplete":      "1-9 edit answer | Left edit last | Answer every question to submit | Esc cancel",
		"classic_chat_3.question.questionnaire_review":       "Review",
		"classic_chat_3.question.questionnaire_review_title": "Review your answers",
		"classic_chat_3.question.questionnaire_unanswered":   "Not answered",
		"classic_chat_3.question.questionnaire_incomplete":   "Answer every question before submitting.",
		"classic_chat_3.question.questionnaire_empty":        "No questions were provided.",
		"classic_chat_3.question.questionnaire_other":        "Other…",
	}, map[string]string{
		"chat_b.question.hint_questionnaire":                 "Arriba/Abajo seleccionar | Intro responder | Izquierda/Derecha o Tab cambiar pregunta | Esc cancelar",
		"chat_b.question.hint_questionnaire_other":           "Escribe una respuesta personalizada | Intro guardar | Esc cerrar editor",
		"chat_b.question.hint_questionnaire_review":          "1-9 editar respuesta | Izquierda editar última | Intro enviar | Esc cancelar",
		"chat_b.question.hint_questionnaire_incomplete":      "1-9 editar respuesta | Izquierda editar última | Responde todo para enviar | Esc cancelar",
		"classic_chat_3.question.questionnaire_review":       "Revisar",
		"classic_chat_3.question.questionnaire_review_title": "Revisa tus respuestas",
		"classic_chat_3.question.questionnaire_unanswered":   "Sin responder",
		"classic_chat_3.question.questionnaire_incomplete":   "Responde todas las preguntas antes de enviar.",
		"classic_chat_3.question.questionnaire_empty":        "No se proporcionaron preguntas.",
		"classic_chat_3.question.questionnaire_other":        "Otra…",
	})
}
