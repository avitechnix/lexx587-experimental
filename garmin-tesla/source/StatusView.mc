import Toybox.Graphics;
import Toybox.Lang;
import Toybox.Time;
import Toybox.WatchUi;

// Battery, range, lock and climate at a glance. Tap for the controls menu.
class StatusView extends WatchUi.View {

    private var _car as Car;

    function initialize(car as Car) {
        View.initialize();
        _car = car;
    }

    function onUpdate(dc as Graphics.Dc) as Void {
        var w = dc.getWidth();
        var h = dc.getHeight();
        var cx = w / 2;
        dc.setColor(Graphics.COLOR_WHITE, Graphics.COLOR_BLACK);
        dc.clear();

        drawText(dc, cx, h * 13 / 100, Graphics.FONT_SMALL, Graphics.COLOR_LT_GRAY, _car.name);

        var battery = _car.battery;
        var batteryText = battery == null ? "--%" : battery + "%";
        drawText(dc, cx, h * 31 / 100, Graphics.FONT_NUMBER_MEDIUM, batteryColor(battery), batteryText);

        var barW = w * 56 / 100;
        var barH = h * 3 / 100;
        var barY = h * 43 / 100;
        dc.setColor(Graphics.COLOR_DK_GRAY, Graphics.COLOR_TRANSPARENT);
        dc.fillRoundedRectangle(cx - barW / 2, barY, barW, barH, barH / 2);
        if (battery != null && battery > 0) {
            dc.setColor(batteryColor(battery), Graphics.COLOR_TRANSPARENT);
            dc.fillRoundedRectangle(cx - barW / 2, barY, barW * (battery > 100 ? 100 : battery) / 100, barH, barH / 2);
        }

        var range = _car.rangeText();
        var rangeLine = range == null ? "" : range;
        if ("Charging".equals(_car.chargingState)) {
            rangeLine += "  Charging";
        }
        drawText(dc, cx, h * 54 / 100, Graphics.FONT_SMALL, Graphics.COLOR_WHITE, rangeLine);

        var locked = _car.locked;
        if (locked != null) {
            drawText(dc, cx, h * 65 / 100, Graphics.FONT_TINY,
                locked ? Graphics.COLOR_GREEN : Graphics.COLOR_ORANGE, locked ? "Locked" : "Unlocked");
        }

        var climate = _car.climateOn == true ? "Climate on" : "Climate off";
        var temp = _car.insideTempText();
        if (temp != null) {
            climate += "  " + temp;
        }
        drawText(dc, cx, h * 74 / 100, Graphics.FONT_TINY,
            _car.climateOn == true ? Graphics.COLOR_BLUE : Graphics.COLOR_LT_GRAY, climate);

        drawText(dc, cx, h * 85 / 100, Graphics.FONT_XTINY, Graphics.COLOR_LT_GRAY, footer());
    }

    private function footer() as String {
        var message = _car.message;
        if (message != null) {
            return message;
        }
        if (_car.asleep) {
            return "Asleep - tap for controls";
        }
        var updatedAt = _car.updatedAt;
        if (updatedAt != null) {
            var minutes = (Time.now().value() - updatedAt) / 60;
            if (minutes > 0) {
                return "Updated " + minutes + " min ago";
            }
        }
        return "Tap for controls";
    }

    private function batteryColor(battery as Number?) as Graphics.ColorType {
        if (battery == null) {
            return Graphics.COLOR_LT_GRAY;
        }
        if (battery <= 20) {
            return Graphics.COLOR_RED;
        }
        if (battery <= 40) {
            return Graphics.COLOR_YELLOW;
        }
        return Graphics.COLOR_GREEN;
    }

    // Long messages wrap onto a second line instead of running off the round screen.
    private function drawText(dc as Graphics.Dc, x as Number, y as Number, font as Graphics.FontType,
            color as Graphics.ColorType, text as String) as Void {
        dc.setColor(color, Graphics.COLOR_TRANSPARENT);
        var maxWidth = dc.getWidth() * 78 / 100;
        if (dc.getTextWidthInPixels(text, font) > maxWidth) {
            text = Graphics.fitTextToArea(text, font, maxWidth, dc.getFontHeight(font) * 2, true);
        }
        dc.drawText(x, y, font, text, Graphics.TEXT_JUSTIFY_CENTER | Graphics.TEXT_JUSTIFY_VCENTER);
    }
}
