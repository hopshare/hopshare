# My Hopshare page use cases

The "My hopShare" page is the central hub of the application for users. It's where we expect them to spend most of their time and where they gain the most benefit of the service. Use cases on this page fall into a few specific categories.

## Select which Organization they are currently working in

* The page should have a pull down with all Organizations so they can select between them quickly. 
* We should also easily link to where they can search/join new Organizations. This must be the only thing on the page if they are not already part of at least one Organization.
* At the top of the page should be a celebration of Request activity in the current Organization- showing a few recent Requests that were completed along with basic metrics (how long it's been around, how many members, how many Requests pending / completed).

## Seeing their current stats

* Current balance of credits
* Number of Requests they've made, along with Date of last one
* Number of Requests they've fulfilled, along with Date of last one

## Making Requests for help

* Should be highly visible how to do this, and we want to encourage Members to do this as often as they can.
* There should be a list of the Member's Requests- this is their 'bank statement' showing pending, canceled, expired and completed Requests. List should be filtered by date range, text, Member.
* Requests should be lifecycled here- Pending can be Canceled or Completed. Completed requests can be selected so comments / pictures can be attached.

## Finding Requests they can help with

* Member should see a list of all unfilled Requests for the current Organization.
* The list should be searchable based on text or Member. You can also have a persistent check box called "Match Me" which will automatically match Requests for the Member when they view the page.


# Having a consistent List component

Because so much of the interaction with hopShare involves going through lists of Requests (either to fulfil them, or to review what you've done), we need a good, reusable list component. While the implementation might be done by different parts of the code, the visual appearance should be the same across all lists to keep things consistent for users. 

we don't expect our lists to have to contain thousands of items, so it is probably safe to just use a scrolling list for now. If the loading times for the lists becomes unbearable, we could move them to a paginated list, but that is extra complexity we can avoid for now.

