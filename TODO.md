# TODO

## Running local Postgres
    podman run --detach \
    --name postgres \
    -e POSTGRES_USER=hopuser \
    -e POSTGRES_PASSWORD=hoppass \
    -e POSTGRES_DB=hopshare \
    -e POSTGRES_ADMIN_PASSWORD=adminpass \
    -v postgres_data:/var/lib/postgresql/data:Z \
    -p 5432:5432 \
    docker.io/library/postgres:17.7


## Bugs

* Login should take username. Usernames should be unique at the service level- do not take email address.
* You can offer to help the same Hop multiple times
* Accepting Help on a Hop that has been Canceled should not be an error- just a message that the Hop was canceled already.
* An Organization Owner can request membership in their own Organization- this should be prevented
* Don't show the "Remove" button on the row for the primary Organization Owner when they go to the Manage Organization page
* Hop Filters say "Created" instead of "Pending" in Your Hops page
* Double check all the messaging on the MyHopShare dashboard- not sure they are correct as Hours numbers change
* Race condition when multiple users sign up at the same time with the same First and Last name. The first one in will win as username must be unique. There is some code in here to detect unique constraint violation but it's not working.


## Now

* Organization "Wall"- closest thing to 'social media' feature- inspire others. A scrolling list of who's helped who recently. Or a "Tag Cloud" of who's helping who?

* Hops are still sort of 'hidden' as far as I'm concerned- how do we make them more front and center??
    * Hops are 'private' by default meaning they are only visible to the requster and helpers. Once Marked 'public' they can be viewed by anyone in the Organization.
    * Hops can have a series of comments attached to them. Comments are shown most recent first. There is a place at the bottom of the Hop page where new comments can be added.
    * Hops can have a set of images attached to them. These are shown as thumbnails in the Hop page, and can be viewed in a Carousel type UI that pops up over the Hop page. The requester and helper of the Hop can add/remove images.
    * Have a separate "Hop" page- where it shows the details and lets the two Members collaborate with comments, pictures, etc...
    * If the Hop is not private, then anyone in tbe Organization can view the Hop and add a comment.
    * Click on any Hop and it goes to that page...with stable URL
    * These can become public pages too- to show off what was done.
    * Re-think how we show "lists" of Hops (or Hop Summaries)
        * We should include avatar images when we show Hop details- make them more visual!!
        * Always clickable to get to the Hop Page

* Change bell icon to an envelope icon in the header

* Drop the 'Home' item on the Header- we should only go to this page if we are not logged in.

* My HopShare Dashboard
    * "Change..." for Organization should be a pulldown of all Organizations Member belongs to. 
    * At bottom of that card, have a link "Find an Organization..." that goes to Find Organization page.
    * Organization card doesn't work well with long names
* Find Organization Page
    * Long Organization names do not fit into the search results- should put those into larger results- with ability to drill into details on the organization before asking to join.
* Joining an Organization should use messages
    * Send an information message to all Owners of an Organization when you request membership. The message body should contain a link that will take the Member directly to their 
    * Send yourself an information message that you requested membership in an Organization.
* Owners are moderators for listings- they can flag/delete inappropriate requests/comments
* Organizations need to have a readable URL for new joiners. A way for users and non-users to sign up quickly.
* Manage Skills on the Member profile page. We will need something for automatic matching...give it some thought. Skills should reside in the database- we can seed some starter ones, but it should grow over time- and be scoped within the organization. We can have these configured for new joiners via a wizard interface.
* Administrator page- see everything, do dangerous stuff. Link conditionally off header menu for Admin users.
* Add in basic monitoring (cron job calling script saving in sqlite):
    * net/http/pprof package (visualize performance)
    * runtime.MemStats / runtime.ReadMemStats() thru a /health endpoint on each golang process
    * select count(*) from pg_stat_activity; (database connections)
    * iostat to see iops levels
    * jq against Caddy logs for traffic levels


Change the "My Organization" panel of the "My Profile" page as follows:
* retitle it to "My Organizations"
* Show a list of all Organizations (logo and full name) the Member is associated with.
    * If the Member is an Owner of the Organization, make the name a clickable link that takes them to the Manage Organization page for that Organization.
    * If the Member is not an Owner of the Organization, follow each Organization row with a placeholder link that says "Leave..." which we will eventually use to let a Member leave that Organization.

## Later

* Add ability to add comments to a completed Request.
* Create a 'celebration' page for the Organization?
* Make service/ExpireHelpRequests() asynchronous- we should start a goroutine that runs daily to clear these out (not only when the myhpopshare page is rendered).



