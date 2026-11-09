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
        var battery = null;
        var charging = false;
        var summary = Application.Storage.getValue("summary");
        if (summary instanceof Dictionary) {
            if (summary["name"] instanceof String) {
                name = summary["name"] as String;
            }
            battery = summary["battery"] as Number?;
            charging = (summary["charging"] as Boolean?) == true;
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

        var w = dc.getWidth();
        var h = dc.getHeight();
        if (dc has :setAntiAlias) {
            dc.setAntiAlias(true);
        }
        dc.setColor(Graphics.COLOR_WHITE, Graphics.COLOR_TRANSPARENT);
        dc.drawText(0, h / 5, Graphics.FONT_GLANCE, name.toUpper(), Graphics.TEXT_JUSTIFY_LEFT | Graphics.TEXT_JUSTIFY_VCENTER);

        var barH = h / 12 > 3 ? h / 12 : 3;
        var barY = h / 2 - barH / 2;
        var barW = w * 85 / 100;
        dc.setColor(0x2C2C2E, Graphics.COLOR_TRANSPARENT);
        dc.fillRoundedRectangle(0, barY, barW, barH, barH / 2);
        if (battery != null && battery > 0) {
            var color = 0x30D158;
            if (!charging && battery <= 20) {
                color = 0xFF453A;
            } else if (!charging && battery <= 40) {
                color = 0xFF9F0A;
            }
            dc.setColor(color, Graphics.COLOR_TRANSPARENT);
            dc.fillRoundedRectangle(0, barY, barW * (battery > 100 ? 100 : battery) / 100, barH, barH / 2);
        }

        dc.setColor(0x8E8E93, Graphics.COLOR_TRANSPARENT);
        dc.drawText(0, h * 4 / 5, Graphics.FONT_GLANCE, detail, Graphics.TEXT_JUSTIFY_LEFT | Graphics.TEXT_JUSTIFY_VCENTER);
    }
}
