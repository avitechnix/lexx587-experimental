import Toybox.Application;
import Toybox.Graphics;
import Toybox.Lang;
import Toybox.WatchUi;

// Shows the battery and lock state saved the last time the app was opened.
(:glance)
class TeslaGlanceView extends WatchUi.GlanceView {

    function initialize() {
        GlanceView.initialize();
    }

    function onUpdate(dc as Graphics.Dc) as Void {
        var name = "My Tesla";
        var detail = "Open to connect";
        var summary = Application.Storage.getValue("summary");
        if (summary instanceof Dictionary) {
            if (summary["name"] instanceof String) {
                name = summary["name"] as String;
            }
            var battery = summary["battery"];
            if (battery != null) {
                detail = battery + "%";
                var range = summary["range"];
                if (range != null) {
                    detail += "  " + range;
                }
                var locked = summary["locked"];
                if (locked != null) {
                    detail += (locked as Boolean) ? "  Locked" : "  Unlocked";
                }
            }
        }

        var h = dc.getHeight();
        dc.setColor(Graphics.COLOR_WHITE, Graphics.COLOR_TRANSPARENT);
        dc.drawText(0, h / 4, Graphics.FONT_GLANCE, name.toUpper(), Graphics.TEXT_JUSTIFY_LEFT | Graphics.TEXT_JUSTIFY_VCENTER);
        dc.setColor(Graphics.COLOR_LT_GRAY, Graphics.COLOR_TRANSPARENT);
        dc.drawText(0, h * 3 / 4, Graphics.FONT_GLANCE, detail, Graphics.TEXT_JUSTIFY_LEFT | Graphics.TEXT_JUSTIFY_VCENTER);
    }
}
