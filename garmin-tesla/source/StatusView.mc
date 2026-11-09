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
        _buttonR = w * 85 / 1000;
        // Outer buttons sit a little higher, following the round edge.
        var xs = [w * 20 / 100, w * 40 / 100, w * 60 / 100, w * 80 / 100];
        var ys = [h * 655 / 1000, h * 68 / 100, h * 68 / 100, h * 655 / 1000];
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

        drawInfo(dc, battery, charging);

        for (var i = 0; i < 4; i++) {
            drawButton(dc, i);
        }

        // Just below the buttons, but no higher than where the screen is wide enough.
        var footerFont = Graphics.FONT_XTINY;
        var footerY = _buttonY[1] + _buttonR + dc.getFontHeight(footerFont) * 3 / 4;
        drawFooter(dc, cx, footerY > h * 86 / 100 ? footerY : h * 86 / 100, footerFont);
    }

    // Name, battery, range and cabin temperature, stacked and centered in the
    // space above the buttons. Measured with the watch's own fonts, stepping
    // the battery digits down a size if they would not fit.
    private function drawInfo(dc as Graphics.Dc, battery as Number?, charging as Boolean) as Void {
        var h = dc.getHeight();
        var cx = dc.getWidth() / 2;
        var top = h * 9 / 100;
        var bottom = _buttonY[0] - _buttonR - h * 2 / 100;

        var temp = _car.insideTempText();
        var nameH = dc.getFontHeight(Graphics.FONT_XTINY);
        var rangeH = dc.getFontHeight(Graphics.FONT_SMALL);
        var tempH = temp != null ? dc.getFontHeight(Graphics.FONT_XTINY) : 0;

        var bigFonts = [Graphics.FONT_NUMBER_MEDIUM, Graphics.FONT_NUMBER_MILD, Graphics.FONT_LARGE];
        var bigFont = Graphics.FONT_LARGE;
        var bigH = 0;
        for (var i = 0; i < bigFonts.size(); i++) {
            bigFont = bigFonts[i];
            // Number fonts carry padding above and below the digits.
            bigH = dc.getFontHeight(bigFont) * (i < 2 ? 78 : 100) / 100;
            if (nameH + bigH + rangeH + tempH <= bottom - top) {
                break;
            }
        }

        var y = top + (bottom - top - (nameH + bigH + rangeH + tempH)) / 2;
        if (y < top) {
            y = top;
        }
        y += nameH / 2;
        dc.setColor(GRAY, Graphics.COLOR_TRANSPARENT);
        dc.drawText(cx, y, Graphics.FONT_XTINY, _car.name.toUpper(),
            Graphics.TEXT_JUSTIFY_CENTER | Graphics.TEXT_JUSTIFY_VCENTER);

        y += nameH / 2 + bigH / 2;
        drawBattery(dc, cx, y, bigFont, bigH, battery, charging);

        y += bigH / 2 + rangeH / 2;
        var range = _car.rangeText();
        dc.setColor(Graphics.COLOR_WHITE, Graphics.COLOR_TRANSPARENT);
        dc.drawText(cx, y, Graphics.FONT_SMALL, range == null ? "--" : range,
            Graphics.TEXT_JUSTIFY_CENTER | Graphics.TEXT_JUSTIFY_VCENTER);

        if (temp != null) {
            y += rangeH / 2 + tempH / 2;
            dc.setColor(GRAY, Graphics.COLOR_TRANSPARENT);
            dc.drawText(cx, y, Graphics.FONT_XTINY, "Inside " + temp,
                Graphics.TEXT_JUSTIFY_CENTER | Graphics.TEXT_JUSTIFY_VCENTER);
        }
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
    // digitsH is the height of the digits themselves, without the font's padding.
    private function drawBattery(dc as Graphics.Dc, cx as Number, y as Number, bigFont as Graphics.FontType,
            digitsH as Number, battery as Number?, charging as Boolean) as Void {
        var digits = battery == null ? "--" : battery.toString();
        var smallFont = Graphics.FONT_SMALL;
        var digitsW = dc.getTextWidthInPixels(digits, bigFont);
        var pctW = dc.getTextWidthInPixels("%", smallFont);
        var left = cx - (digitsW + pctW) / 2;

        dc.setColor(Graphics.COLOR_WHITE, Graphics.COLOR_TRANSPARENT);
        dc.drawText(left, y, bigFont, digits, Graphics.TEXT_JUSTIFY_LEFT | Graphics.TEXT_JUSTIFY_VCENTER);
        dc.setColor(GRAY, Graphics.COLOR_TRANSPARENT);
        // Top-aligned with the digits.
        var pctY = y - digitsH / 2 + dc.getFontHeight(smallFont) * 2 / 5;
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
        // The buttons carry no text labels: four would not fit across a round
        // screen, and tapping one names its action in the footer.
        var fill = BUTTON;
        var iconColor = Graphics.COLOR_WHITE;
        var commands = [] as Array<String>;

        if (button == BUTTON_LOCK) {
            var locked = _car.locked;
            if (locked == false) {
                fill = ORANGE;
                iconColor = Graphics.COLOR_BLACK;
            } else if (locked == true) {
                iconColor = GREEN;
            }
            commands = ["lock", "unlock"];
        } else if (button == BUTTON_CLIMATE) {
            if (_car.climateOn == true) {
                fill = BLUE;
            }
            commands = ["start_climate", "stop_climate"];
        } else if (button == BUTTON_FRUNK) {
            if (_car.frunkOpen) {
                fill = ORANGE;
                iconColor = Graphics.COLOR_BLACK;
            }
            commands = ["activate_front_trunk"];
        } else {
            if (_car.trunkOpen) {
                fill = ORANGE;
                iconColor = Graphics.COLOR_BLACK;
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
    }

    private function drawFooter(dc as Graphics.Dc, cx as Number, y as Number, font as Graphics.FontType) as Void {
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
                        text = "Updated " + minutes + " min ago";
                    }
                }
            }
        }
        // One line, as wide as the round screen allows at this height.
        var half = dc.getWidth() / 2;
        var dy = y - dc.getHeight() / 2;
        var maxW = Math.sqrt(half * half - dy * dy).toNumber() * 2 * 85 / 100;
        if (dc.getTextWidthInPixels(text, font) > maxW) {
            var fitted = Graphics.fitTextToArea(text, font, maxW, dc.getFontHeight(font), true);
            if (fitted != null && fitted.length() > 0) {
                text = fitted;
            }
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
