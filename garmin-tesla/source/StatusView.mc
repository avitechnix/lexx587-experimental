import Toybox.Graphics;
import Toybox.Lang;
import Toybox.Math;
import Toybox.Time;
import Toybox.Timer;
import Toybox.WatchUi;

// Home screen: a battery ring around the edge, the car's numbers in the
// middle and four quick-action buttons along the bottom.
class StatusView extends WatchUi.View {

    // Quick-action buttons, left to right.
    enum {
        BUTTON_LOCK,
        BUTTON_CLIMATE,
        BUTTON_FRUNK,
        BUTTON_TRUNK
    }

    private const GREEN = 0x30D158;
    private const ORANGE = 0xFF9F0A;
    private const RED = 0xFF453A;
    private const BLUE = 0x0A84FF;
    private const GRAY = 0x8E8E93;
    private const TRACK = 0x2C2C2E;
    private const BUTTON = 0x1C1C1E;

    private var _car as Car;
    private var _timer as Timer.Timer = new Timer.Timer();
    private var _spin as Number = 0;

    private var _buttonX as Array<Number> = [0, 0, 0, 0];
    private var _buttonY as Array<Number> = [0, 0, 0, 0];
    private var _buttonR as Number = 0;

    function initialize(car as Car) {
        View.initialize();
        _car = car;
    }

    function onLayout(dc as Graphics.Dc) as Void {
        var w = dc.getWidth();
        var h = dc.getHeight();
        _buttonR = w * 8 / 100;
        // Outer buttons sit a little higher, following the round edge.
        var xs = [w * 215 / 1000, w * 405 / 1000, w * 595 / 1000, w * 785 / 1000];
        var ys = [h * 635 / 1000, h * 66 / 100, h * 66 / 100, h * 635 / 1000];
        for (var i = 0; i < 4; i++) {
            _buttonX[i] = xs[i];
            _buttonY[i] = ys[i];
        }
    }

    function onShow() as Void {
        _timer.start(method(:onTick), 80, true);
    }

    function onHide() as Void {
        _timer.stop();
    }

    // Animates the spinner on the button whose command is in flight.
    function onTick() as Void {
        if (_car.busy) {
            _spin = (_spin + 24) % 360;
            WatchUi.requestUpdate();
        }
    }

    // Index of the quick-action button at (x, y), or -1.
    function buttonAt(x as Number, y as Number) as Number {
        // A slightly larger hit area than the drawn circle, for fingertips.
        var reach = _buttonR * 13 / 10;
        for (var i = 0; i < 4; i++) {
            var dx = x - _buttonX[i];
            var dy = y - _buttonY[i];
            if (dx * dx + dy * dy <= reach * reach) {
                return i;
            }
        }
        return -1;
    }

    function onUpdate(dc as Graphics.Dc) as Void {
        var w = dc.getWidth();
        var h = dc.getHeight();
        var cx = w / 2;
        if (dc has :setAntiAlias) {
            dc.setAntiAlias(true);
        }
        dc.setColor(Graphics.COLOR_WHITE, Graphics.COLOR_BLACK);
        dc.clear();

        var battery = _car.battery;
        var charging = "Charging".equals(_car.chargingState);
        drawBatteryRing(dc, battery, charging);

        dc.setColor(GRAY, Graphics.COLOR_TRANSPARENT);
        dc.drawText(cx, h * 14 / 100, Graphics.FONT_XTINY, _car.name.toUpper(),
            Graphics.TEXT_JUSTIFY_CENTER | Graphics.TEXT_JUSTIFY_VCENTER);

        drawBattery(dc, cx, h * 285 / 1000, battery, charging);

        var range = _car.rangeText();
        dc.setColor(Graphics.COLOR_WHITE, Graphics.COLOR_TRANSPARENT);
        dc.drawText(cx, h * 41 / 100, Graphics.FONT_SMALL, range == null ? "--" : range,
            Graphics.TEXT_JUSTIFY_CENTER | Graphics.TEXT_JUSTIFY_VCENTER);

        var temp = _car.insideTempText();
        if (temp != null) {
            dc.setColor(GRAY, Graphics.COLOR_TRANSPARENT);
            dc.drawText(cx, h * 48 / 100, Graphics.FONT_XTINY, "Inside " + temp,
                Graphics.TEXT_JUSTIFY_CENTER | Graphics.TEXT_JUSTIFY_VCENTER);
        }

        for (var i = 0; i < 4; i++) {
            drawButton(dc, i);
        }

        drawFooter(dc, cx, h * 875 / 1000);
    }

    private function drawBatteryRing(dc as Graphics.Dc, battery as Number?, charging as Boolean) as Void {
        var w = dc.getWidth();
        var cx = w / 2;
        var cy = dc.getHeight() / 2;
        var pen = w / 40;
        var r = cx - pen;

        // A 220 degree track that opens at the bottom, around the buttons,
        // filled clockwise from the lower left.
        dc.setPenWidth(pen);
        dc.setColor(TRACK, Graphics.COLOR_TRANSPARENT);
        dc.drawArc(cx, cy, r, Graphics.ARC_CLOCKWISE, 200, 340);
        roundCap(dc, cx, cy, r, 200, pen);
        roundCap(dc, cx, cy, r, 340, pen);

        if (battery != null && battery > 0) {
            var pct = battery > 100 ? 100 : battery;
            var end = 200 - 220 * pct / 100;
            dc.setColor(charging ? GREEN : batteryColor(pct), Graphics.COLOR_TRANSPARENT);
            if (pct < 100) {
                dc.drawArc(cx, cy, r, Graphics.ARC_CLOCKWISE, 200, (end + 360) % 360);
            } else {
                dc.drawArc(cx, cy, r, Graphics.ARC_CLOCKWISE, 200, 340);
            }
            roundCap(dc, cx, cy, r, 200, pen);
            roundCap(dc, cx, cy, r, end, pen);
        }
        dc.setPenWidth(1);
    }

    private function roundCap(dc as Graphics.Dc, cx as Number, cy as Number, r as Number, degrees as Number, pen as Number) as Void {
        var a = Math.toRadians(degrees);
        dc.fillCircle(cx + r * Math.cos(a), cy - r * Math.sin(a), pen / 2);
    }

    // The percentage in large digits, with a smaller % sign and a bolt while charging.
    private function drawBattery(dc as Graphics.Dc, cx as Number, y as Number, battery as Number?, charging as Boolean) as Void {
        var digits = battery == null ? "--" : battery.toString();
        var bigFont = Graphics.FONT_NUMBER_MEDIUM;
        var smallFont = Graphics.FONT_SMALL;
        var digitsW = dc.getTextWidthInPixels(digits, bigFont);
        var pctW = dc.getTextWidthInPixels("%", smallFont);
        var left = cx - (digitsW + pctW) / 2;

        dc.setColor(Graphics.COLOR_WHITE, Graphics.COLOR_TRANSPARENT);
        dc.drawText(left, y, bigFont, digits, Graphics.TEXT_JUSTIFY_LEFT | Graphics.TEXT_JUSTIFY_VCENTER);
        dc.setColor(GRAY, Graphics.COLOR_TRANSPARENT);
        var pctY = y - dc.getFontHeight(bigFont) / 5;
        dc.drawText(left + digitsW + 2, pctY, smallFont, "%", Graphics.TEXT_JUSTIFY_LEFT | Graphics.TEXT_JUSTIFY_VCENTER);

        if (charging) {
            var s = dc.getFontHeight(smallFont);
            dc.setColor(GREEN, Graphics.COLOR_TRANSPARENT);
            Icons.bolt(dc, left - s * 2 / 3, y, s);
        }
    }

    private function drawButton(dc as Graphics.Dc, button as Number) as Void {
        var x = _buttonX[button];
        var y = _buttonY[button];
        var r = _buttonR;
        var iconSize = r;

        // A filled color means "needs attention or running"; dark means idle.
        var fill = BUTTON;
        var iconColor = Graphics.COLOR_WHITE;
        var label = "";
        var commands = [] as Array<String>;

        if (button == BUTTON_LOCK) {
            var locked = _car.locked;
            if (locked == false) {
                fill = ORANGE;
                iconColor = Graphics.COLOR_BLACK;
                label = "Unlocked";
            } else {
                iconColor = locked == true ? GREEN : Graphics.COLOR_WHITE;
                label = locked == true ? "Locked" : "Lock";
            }
            commands = ["lock", "unlock"];
        } else if (button == BUTTON_CLIMATE) {
            if (_car.climateOn == true) {
                fill = BLUE;
                label = "On";
            } else {
                label = "Climate";
            }
            commands = ["start_climate", "stop_climate"];
        } else if (button == BUTTON_FRUNK) {
            if (_car.frunkOpen) {
                fill = ORANGE;
                iconColor = Graphics.COLOR_BLACK;
                label = "Open";
            } else {
                label = "Frunk";
            }
            commands = ["activate_front_trunk"];
        } else {
            if (_car.trunkOpen) {
                fill = ORANGE;
                iconColor = Graphics.COLOR_BLACK;
                label = "Open";
            } else {
                label = "Trunk";
            }
            commands = ["activate_rear_trunk"];
        }

        dc.setColor(fill, Graphics.COLOR_TRANSPARENT);
        dc.fillCircle(x, y, r);
        if (fill == BUTTON) {
            dc.setPenWidth(2);
            dc.setColor(TRACK, Graphics.COLOR_TRANSPARENT);
            dc.drawCircle(x, y, r);
            dc.setPenWidth(1);
        }

        dc.setColor(iconColor, Graphics.COLOR_TRANSPARENT);
        if (button == BUTTON_LOCK) {
            Icons.padlock(dc, x, y, iconSize, _car.locked == false);
        } else if (button == BUTTON_CLIMATE) {
            Icons.fan(dc, x, y, iconSize);
        } else {
            Icons.car(dc, x, y, iconSize, button == BUTTON_FRUNK);
        }

        var pending = _car.pending;
        if (pending != null && commands.indexOf(pending) >= 0) {
            var pen = r / 6;
            dc.setPenWidth(pen);
            dc.setColor(Graphics.COLOR_WHITE, Graphics.COLOR_TRANSPARENT);
            dc.drawArc(x, y, r + pen, Graphics.ARC_CLOCKWISE, (360 - _spin) % 360, (270 - _spin + 360) % 360);
            dc.setPenWidth(1);
        }

        dc.setColor(GRAY, Graphics.COLOR_TRANSPARENT);
        dc.drawText(x, y + r + dc.getFontHeight(Graphics.FONT_XTINY) * 3 / 5, Graphics.FONT_XTINY, label,
            Graphics.TEXT_JUSTIFY_CENTER | Graphics.TEXT_JUSTIFY_VCENTER);
    }

    private function drawFooter(dc as Graphics.Dc, cx as Number, y as Number) as Void {
        var text = _car.message;
        var color = Graphics.COLOR_WHITE;
        if (text == null) {
            color = GRAY;
            text = "Tap for more";
            if (_car.asleep) {
                text = "Asleep";
            } else {
                var updatedAt = _car.updatedAt;
                if (updatedAt != null) {
                    var minutes = (Time.now().value() - updatedAt) / 60;
                    if (minutes > 0) {
                        text = minutes + " min ago";
                    }
                }
            }
        }
        // The screen narrows toward the bottom, so long messages wrap and shrink.
        var font = Graphics.FONT_XTINY;
        var maxW = dc.getWidth() * 56 / 100;
        if (dc.getTextWidthInPixels(text, font) > maxW) {
            text = Graphics.fitTextToArea(text, font, maxW, dc.getFontHeight(font) * 2, true);
        }
        dc.setColor(color, Graphics.COLOR_TRANSPARENT);
        dc.drawText(cx, y, font, text, Graphics.TEXT_JUSTIFY_CENTER | Graphics.TEXT_JUSTIFY_VCENTER);
    }

    private function batteryColor(battery as Number) as Number {
        if (battery <= 20) {
            return RED;
        }
        if (battery <= 40) {
            return ORANGE;
        }
        return GREEN;
    }
}
