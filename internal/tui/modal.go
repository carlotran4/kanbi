package tui

// modalKind is the single precedence source for transient TUI overlays. It is
// deliberately only a selector: individual modals retain their own state,
// input handling, and rendering.
type modalKind uint8

const (
	modalNone modalKind = iota
	modalIntegration
	modalFocusSettings
	modalFocusReplace
	modalPause
	modalResume
	modalOnboarding
	modalHelp
	modalBoardRename
	modalBoardWorktreeEnable
	modalBoardEdit
	modalBoardDelete
	modalBoardExport
	modalBoardImport
	modalBoardPicker
	modalMasterFilter
	modalEdit
	modalStateMenu
	modalColumnEdit
	modalPromptFallback
	modalRepair
	modalBranchName
	modalWorkspaceIntegration
)

// activeModalKind preserves the visible overlay order. Update and View must
// both select from this method so a covered modal cannot consume input.
func (m Model) activeModalKind() modalKind {
	switch {
	case m.integrationOpen:
		return modalIntegration
	case m.focusSettingsOpen:
		return modalFocusSettings
	case m.focusReplaceOpen:
		return modalFocusReplace
	case m.pauseOpen:
		return modalPause
	case m.resumeOpen:
		return modalResume
	case m.firstRun:
		return modalOnboarding
	case m.showHelp:
		return modalHelp
	case m.boardRenaming:
		return modalBoardRename
	case m.boardWorktreeEnabling:
		return modalBoardWorktreeEnable
	case m.boardEditing:
		return modalBoardEdit
	case m.boardDeleting:
		return modalBoardDelete
	case m.boardExporting:
		return modalBoardExport
	case m.boardImporting:
		return modalBoardImport
	case m.boardPicker:
		return modalBoardPicker
	case m.masterFilterOpen:
		return modalMasterFilter
	case m.editing:
		return modalEdit
	case m.stateMenu:
		return modalStateMenu
	case m.columnEditing:
		return modalColumnEdit
	case m.promptFallback:
		return modalPromptFallback
	case m.repairing:
		return modalRepair
	case m.branchNaming:
		return modalBranchName
	case m.workspaceIntegrating:
		return modalWorkspaceIntegration
	default:
		return modalNone
	}
}
