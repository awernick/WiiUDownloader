package main

import (
	"fmt"
	"log"

	wiiudownloader "github.com/Xpl0itU/WiiUDownloader"
	"github.com/gotk3/gotk3/gtk"
)

// showVersionSelectionDialog prompts the user to choose a specific title
// version. It returns the selected version and true when the user accepts.
// A version of 0 uses the default TMD (not a specific version).
func showVersionSelectionDialog(parent *gtk.Window, title wiiudownloader.TitleEntry) (int, bool) {
	dialog, err := gtk.DialogNew()
	if err != nil {
		log.Printf("failed to create version selection dialog: %v", err)
		return 0, false
	}
	defer dialog.Destroy()

	dialog.SetTitle("Select Title Version")
	dialog.SetModal(true)
	if parent != nil {
		dialog.SetTransientFor(parent)
	}
	dialog.SetPosition(gtk.WIN_POS_CENTER_ON_PARENT)
	dialog.AddButton("Cancel", gtk.RESPONSE_CANCEL)
	dialog.AddButton("OK", gtk.RESPONSE_OK)
	dialog.SetDefaultResponse(gtk.RESPONSE_OK)

	contentArea, err := dialog.GetContentArea()
	if err != nil {
		log.Printf("failed to get version dialog content area: %v", err)
		return 0, false
	}
	contentArea.SetSpacing(12)
	contentArea.SetMarginTop(18)
	contentArea.SetMarginBottom(18)
	contentArea.SetMarginStart(18)
	contentArea.SetMarginEnd(18)

	titleLabel, err := gtk.LabelNew("")
	if err == nil {
		titleText := fmt.Sprintf("%s (%s) - %016x", escapeMarkup(title.Name), escapeMarkup(wiiudownloader.GetFormattedRegion(title.Region)), title.TitleID)
		titleLabel.SetMarkup(fmt.Sprintf("<span size='large' weight='bold'>%s</span>", titleText))
		titleLabel.SetHAlign(gtk.ALIGN_START)
		titleLabel.SetLineWrap(true)
		titleLabel.SetMaxWidthChars(50)
		contentArea.PackStart(titleLabel, false, false, 0)
	}

	descLabel, err := gtk.LabelNew("Enter the version number for this title. A value of 0 uses the default TMD (not a specific version).")
	if err == nil {
		descLabel.SetLineWrap(true)
		descLabel.SetHAlign(gtk.ALIGN_START)
		contentArea.PackStart(descLabel, false, false, 0)
	}

	var spinButton *gtk.SpinButton
	spinBox, err := gtk.BoxNew(gtk.ORIENTATION_HORIZONTAL, 12)
	if err == nil {
		spinBox.SetHAlign(gtk.ALIGN_START)

		versionLabel, _ := gtk.LabelNew("Version:")
		spinBox.PackStart(versionLabel, false, false, 0)

		adjustment, _ := gtk.AdjustmentNew(0, 0, 65535, 1, 10, 0)
		spinButton, _ = gtk.SpinButtonNew(adjustment, 1, 0)
		spinButton.SetNumeric(true)
		spinButton.SetWidthChars(8)
		spinBox.PackStart(spinButton, false, false, 0)

		contentArea.PackStart(spinBox, false, false, 0)
	}

	linkBox, err := gtk.BoxNew(gtk.ORIENTATION_HORIZONTAL, 4)
	if err == nil {
		linkBox.SetHAlign(gtk.ALIGN_START)

		linkLabel, _ := gtk.LabelNew("You can find a list of available versions on the")
		wikiLinkBtn, _ := gtk.LinkButtonNewWithLabel("https://wiiubrew.org/wiki/Title_database", "WiiUBrew Title Database")
		wikiLinkBtn.SetRelief(gtk.RELIEF_NONE)
		wikiLinkBtn.SetMarginStart(0)
		wikiLinkBtn.SetMarginEnd(0)

		linkBox.PackStart(linkLabel, false, false, 0)
		linkBox.PackStart(wikiLinkBtn, false, false, 0)
		contentArea.PackStart(linkBox, false, false, 0)
	}

	hintLabel, err := gtk.LabelNew("")
	if err == nil {
		hintLabel.SetMarkup("<span size='small' alpha='70%'>Tip: You can change this later by clicking the Version column in the queue.</span>")
		hintLabel.SetLineWrap(true)
		hintLabel.SetHAlign(gtk.ALIGN_START)
		contentArea.PackStart(hintLabel, false, false, 0)
	}

	contentArea.ShowAll()

	response := dialog.Run()
	if response != gtk.RESPONSE_OK || spinButton == nil {
		return 0, false
	}

	return spinButton.GetValueAsInt(), true
}
